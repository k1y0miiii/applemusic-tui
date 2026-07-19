#include "coreaudio_darwin.h"

#import <CoreAudio/AudioHardware.h>
#import <CoreAudio/AudioHardwareTapping.h>
#import <CoreAudio/CATapDescription.h>
#import <Foundation/Foundation.h>

#include <ctype.h>
#include <limits.h>
#include <math.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef enum {
    AMTUI_SAMPLE_FLOAT32 = AMTUI_INTERNAL_SAMPLE_FLOAT32,
    AMTUI_SAMPLE_FLOAT64 = AMTUI_INTERNAL_SAMPLE_FLOAT64,
    AMTUI_SAMPLE_PCM16 = AMTUI_INTERNAL_SAMPLE_PCM16,
} amtui_sample_kind;

struct amtui_tap {
    AudioObjectID tap_id;
    AudioObjectID aggregate_id;
    AudioDeviceIOProcID io_proc_id;
    bool started;

    AudioStreamBasicDescription asbd;
    amtui_sample_kind sample_kind;
    uint32_t input_channels;
    uint32_t output_channels;
    size_t bytes_per_sample;
    bool non_interleaved;

    float *ring;
    size_t ring_capacity;
    _Atomic uint64_t write_index;
    _Atomic uint64_t read_index;
};

static const char *const amtui_permission_hint =
    "Allow System Audio Recording in System Settings > Privacy & Security, "
    "or run from a signed app bundle with NSAudioCaptureUsageDescription";

static bool amtui_status_is_fourcc(OSStatus status) {
    uint32_t value = (uint32_t)status;
    for (unsigned shift = 0; shift <= 24; shift += 8) {
        if (!isprint((unsigned char)((value >> shift) & 0xffu))) {
            return false;
        }
    }
    return true;
}

static void amtui_set_error(
    char *err,
    size_t errlen,
    const char *operation,
    OSStatus status,
    const char *detail
) {
    if (err == NULL || errlen == 0) {
        return;
    }

    const char *separator = detail != NULL && detail[0] != '\0' ? ": " : "";
    const char *message = detail != NULL ? detail : "";
    if (amtui_status_is_fourcc(status)) {
        uint32_t value = (uint32_t)status;
        snprintf(
            err,
            errlen,
            "%s failed (OSStatus=%d, '%c%c%c%c')%s%s. %s.",
            operation,
            (int)status,
            (char)((value >> 24) & 0xffu),
            (char)((value >> 16) & 0xffu),
            (char)((value >> 8) & 0xffu),
            (char)(value & 0xffu),
            separator,
            message,
            amtui_permission_hint
        );
    } else {
        snprintf(
            err,
            errlen,
            "%s failed (OSStatus=%d)%s%s. %s.",
            operation,
            (int)status,
            separator,
            message,
            amtui_permission_hint
        );
    }
}

static void amtui_describe_asbd(
    const AudioStreamBasicDescription *asbd,
    const char *reason,
    char *detail,
    size_t detail_len
) {
    snprintf(
        detail,
        detail_len,
        "%s; ASBD format=0x%08x flags=0x%08x rate=%.3f channels=%u "
        "bits=%u bytesPerFrame=%u framesPerPacket=%u",
        reason,
        (unsigned)asbd->mFormatID,
        (unsigned)asbd->mFormatFlags,
        asbd->mSampleRate,
        (unsigned)asbd->mChannelsPerFrame,
        (unsigned)asbd->mBitsPerChannel,
        (unsigned)asbd->mBytesPerFrame,
        (unsigned)asbd->mFramesPerPacket
    );
}

static OSStatus amtui_configure_format(
    amtui_tap *tap,
    const AudioStreamBasicDescription *asbd,
    char *detail,
    size_t detail_len
) {
    if (!isfinite(asbd->mSampleRate) || asbd->mSampleRate <= 0.0) {
        amtui_describe_asbd(asbd, "invalid sample rate", detail, detail_len);
        return kAudioDeviceUnsupportedFormatError;
    }
    if (asbd->mChannelsPerFrame == 0) {
        amtui_describe_asbd(asbd, "zero channels", detail, detail_len);
        return kAudioDeviceUnsupportedFormatError;
    }
    if (asbd->mFormatID != kAudioFormatLinearPCM) {
        amtui_describe_asbd(asbd, "only linear PCM is supported", detail, detail_len);
        return kAudioDeviceUnsupportedFormatError;
    }

    AudioFormatFlags flags = asbd->mFormatFlags;
    if ((flags & kAudioFormatFlagIsBigEndian) != 0 ||
        (flags & kAudioFormatFlagIsAlignedHigh) != 0 ||
        (flags & kAudioFormatFlagIsPacked) == 0) {
        amtui_describe_asbd(
            asbd,
            "only native-endian packed samples are supported",
            detail,
            detail_len
        );
        return kAudioDeviceUnsupportedFormatError;
    }

    size_t bytes_per_sample = 0;
    amtui_sample_kind kind;
    if ((flags & kAudioFormatFlagIsFloat) != 0 &&
        (flags & kAudioFormatFlagIsSignedInteger) == 0 &&
        asbd->mBitsPerChannel == 32) {
        kind = AMTUI_SAMPLE_FLOAT32;
        bytes_per_sample = sizeof(float);
    } else if ((flags & kAudioFormatFlagIsFloat) != 0 &&
               (flags & kAudioFormatFlagIsSignedInteger) == 0 &&
               asbd->mBitsPerChannel == 64) {
        kind = AMTUI_SAMPLE_FLOAT64;
        bytes_per_sample = sizeof(double);
    } else if ((flags & kAudioFormatFlagIsFloat) == 0 &&
               (flags & kAudioFormatFlagIsSignedInteger) != 0 &&
               asbd->mBitsPerChannel == 16) {
        kind = AMTUI_SAMPLE_PCM16;
        bytes_per_sample = sizeof(int16_t);
    } else {
        amtui_describe_asbd(
            asbd,
            "supported encodings are Float32, Float64, and signed PCM16",
            detail,
            detail_len
        );
        return kAudioDeviceUnsupportedFormatError;
    }

    bool non_interleaved =
        (flags & kAudioFormatFlagIsNonInterleaved) != 0;
    uint64_t expected_bytes_per_frame = bytes_per_sample;
    if (!non_interleaved) {
        expected_bytes_per_frame *= asbd->mChannelsPerFrame;
    }
    if (expected_bytes_per_frame > UINT32_MAX ||
        asbd->mBytesPerFrame != (UInt32)expected_bytes_per_frame) {
        amtui_describe_asbd(
            asbd,
            "unexpected bytes-per-frame layout",
            detail,
            detail_len
        );
        return kAudioDeviceUnsupportedFormatError;
    }

    tap->asbd = *asbd;
    tap->sample_kind = kind;
    tap->input_channels = asbd->mChannelsPerFrame;
    tap->output_channels =
        asbd->mChannelsPerFrame > 2 ? 2 : asbd->mChannelsPerFrame;
    tap->bytes_per_sample = bytes_per_sample;
    tap->non_interleaved = non_interleaved;
    return noErr;
}

static inline bool amtui_ring_reserve(
    amtui_tap *tap,
    size_t sample_count,
    uint64_t *write_index
) {
    uint64_t write =
        atomic_load_explicit(&tap->write_index, memory_order_relaxed);
    uint64_t read =
        atomic_load_explicit(&tap->read_index, memory_order_acquire);
    uint64_t used = write - read;
    if (used > tap->ring_capacity ||
        sample_count > tap->ring_capacity - (size_t)used) {
        return false;
    }
    *write_index = write;
    return true;
}

static inline void amtui_ring_write(
    amtui_tap *tap,
    uint64_t write_index,
    size_t offset,
    float sample
) {
    size_t ring_index =
        (size_t)((write_index + offset) % tap->ring_capacity);
    tap->ring[ring_index] = sample;
}

static inline void amtui_ring_commit(
    amtui_tap *tap,
    uint64_t write_index,
    size_t sample_count
) {
    atomic_store_explicit(
        &tap->write_index,
        write_index + sample_count,
        memory_order_release
    );
}

static bool amtui_ring_push_samples(
    amtui_tap *tap,
    const float *samples,
    size_t sample_count
) {
    uint64_t write = 0;
    if (!amtui_ring_reserve(tap, sample_count, &write)) {
        return false;
    }
    for (size_t index = 0; index < sample_count; index++) {
        amtui_ring_write(tap, write, index, samples[index]);
    }
    amtui_ring_commit(tap, write, sample_count);
    return true;
}

static int amtui_ring_read_samples(
    amtui_tap *tap,
    float *dst,
    int max_samples
) {
    if (tap == NULL || dst == NULL || max_samples <= 0 ||
        tap->ring == NULL || tap->ring_capacity == 0) {
        return 0;
    }

    uint64_t read =
        atomic_load_explicit(&tap->read_index, memory_order_relaxed);
    uint64_t write =
        atomic_load_explicit(&tap->write_index, memory_order_acquire);
    uint64_t available = write - read;
    if (available == 0) {
        return 0;
    }
    if (available > tap->ring_capacity) {
        available = tap->ring_capacity;
    }

    size_t count = (size_t)available;
    if (count > (size_t)max_samples) {
        count = (size_t)max_samples;
    }
    size_t ring_offset = (size_t)(read % tap->ring_capacity);
    size_t first = tap->ring_capacity - ring_offset;
    if (first > count) {
        first = count;
    }
    memcpy(dst, tap->ring + ring_offset, first * sizeof(float));
    if (first < count) {
        memcpy(
            dst + first,
            tap->ring,
            (count - first) * sizeof(float)
        );
    }
    atomic_store_explicit(
        &tap->read_index,
        read + count,
        memory_order_release
    );
    return (int)count;
}

static inline float amtui_convert_sample(
    const uint8_t *sample,
    amtui_sample_kind kind
) {
    switch (kind) {
        case AMTUI_SAMPLE_FLOAT32: {
            float value;
            memcpy(&value, sample, sizeof(value));
            return value;
        }
        case AMTUI_SAMPLE_FLOAT64: {
            double value;
            memcpy(&value, sample, sizeof(value));
            return (float)value;
        }
        case AMTUI_SAMPLE_PCM16: {
            int16_t value;
            memcpy(&value, sample, sizeof(value));
            return (float)value / 32768.0f;
        }
    }
    return 0.0f;
}

static size_t amtui_convert_and_push(
    amtui_tap *tap,
    const AudioBufferList *in_input_data
) {
    if (tap == NULL || in_input_data == NULL ||
        in_input_data->mNumberBuffers == 0) {
        return 0;
    }

    size_t frames = SIZE_MAX;
    const AudioBuffer *interleaved_buffer = NULL;
    if (tap->non_interleaved) {
        if (in_input_data->mNumberBuffers < tap->output_channels) {
            return 0;
        }
        for (uint32_t channel = 0; channel < tap->output_channels; channel++) {
            const AudioBuffer *buffer = &in_input_data->mBuffers[channel];
            if (buffer->mData == NULL || buffer->mNumberChannels != 1) {
                return 0;
            }
            size_t channel_frames =
                buffer->mDataByteSize / tap->asbd.mBytesPerFrame;
            if (channel_frames < frames) {
                frames = channel_frames;
            }
        }
    } else {
        for (UInt32 index = 0; index < in_input_data->mNumberBuffers; index++) {
            const AudioBuffer *candidate = &in_input_data->mBuffers[index];
            if (candidate->mData != NULL &&
                candidate->mDataByteSize != 0 &&
                candidate->mNumberChannels == tap->output_channels) {
                interleaved_buffer = candidate;
                break;
            }
        }
        if (interleaved_buffer == NULL) {
            return 0;
        }
        frames =
            interleaved_buffer->mDataByteSize / tap->asbd.mBytesPerFrame;
    }

    if (frames == 0 || frames == SIZE_MAX ||
        frames > SIZE_MAX / tap->output_channels) {
        return 0;
    }
    size_t sample_count = frames * tap->output_channels;
    uint64_t write = 0;
    if (!amtui_ring_reserve(tap, sample_count, &write)) {
        return 0;
    }

    size_t output_index = 0;
    for (size_t frame = 0; frame < frames; frame++) {
        for (uint32_t channel = 0; channel < tap->output_channels; channel++) {
            const uint8_t *sample;
            if (tap->non_interleaved) {
                const AudioBuffer *buffer =
                    &in_input_data->mBuffers[channel];
                sample = (const uint8_t *)buffer->mData +
                    frame * tap->asbd.mBytesPerFrame;
            } else {
                sample = (const uint8_t *)interleaved_buffer->mData +
                    frame * tap->asbd.mBytesPerFrame +
                    channel * tap->bytes_per_sample;
            }
            amtui_ring_write(
                tap,
                write,
                output_index,
                amtui_convert_sample(sample, tap->sample_kind)
            );
            output_index++;
        }
    }

    amtui_ring_commit(tap, write, sample_count);
    return sample_count;
}

static OSStatus amtui_io_proc(
    AudioObjectID in_device,
    const AudioTimeStamp *in_now,
    const AudioBufferList *in_input_data,
    const AudioTimeStamp *in_input_time,
    AudioBufferList *out_output_data,
    const AudioTimeStamp *in_output_time,
    void *in_client_data
) {
    (void)in_device;
    (void)in_now;
    (void)in_input_time;
    (void)out_output_data;
    (void)in_output_time;

    amtui_convert_and_push((amtui_tap *)in_client_data, in_input_data);
    return noErr;
}

static OSStatus amtui_validate_aggregate_input(
    AudioObjectID aggregate_id,
    uint32_t expected_channels,
    char *detail,
    size_t detail_len
) {
    AudioObjectPropertyAddress address = {
        .mSelector = kAudioDevicePropertyStreamConfiguration,
        .mScope = kAudioDevicePropertyScopeInput,
        .mElement = kAudioObjectPropertyElementMain,
    };
    UInt32 property_size = 0;
    OSStatus status = AudioObjectGetPropertyDataSize(
        aggregate_id,
        &address,
        0,
        NULL,
        &property_size
    );
    if (status != noErr) {
        snprintf(
            detail,
            detail_len,
            "cannot query aggregate input stream configuration size"
        );
        return status;
    }

    size_t header_size = offsetof(AudioBufferList, mBuffers);
    if (property_size < header_size) {
        snprintf(
            detail,
            detail_len,
            "malformed aggregate input stream configuration (%u bytes)",
            (unsigned)property_size
        );
        return kAudioHardwareBadPropertySizeError;
    }

    AudioBufferList *configuration =
        (AudioBufferList *)calloc(1, property_size);
    if (configuration == NULL) {
        snprintf(
            detail,
            detail_len,
            "cannot allocate aggregate input stream configuration"
        );
        return kAudioHardwareUnspecifiedError;
    }
    status = AudioObjectGetPropertyData(
        aggregate_id,
        &address,
        0,
        NULL,
        &property_size,
        configuration
    );
    if (status != noErr) {
        snprintf(
            detail,
            detail_len,
            "cannot read aggregate input stream configuration"
        );
        free(configuration);
        return status;
    }

    size_t max_buffers =
        (property_size - header_size) / sizeof(AudioBuffer);
    if (configuration->mNumberBuffers > max_buffers) {
        snprintf(
            detail,
            detail_len,
            "malformed aggregate input buffer count %u for %u bytes",
            (unsigned)configuration->mNumberBuffers,
            (unsigned)property_size
        );
        free(configuration);
        return kAudioHardwareBadPropertySizeError;
    }

    uint64_t actual_channels = 0;
    for (UInt32 index = 0; index < configuration->mNumberBuffers; index++) {
        actual_channels += configuration->mBuffers[index].mNumberChannels;
    }
    free(configuration);

    if (actual_channels != expected_channels) {
        snprintf(
            detail,
            detail_len,
            "aggregate input exposes %llu channels, but the process tap exposes "
            "%u; refusing capture because extra hardware input could be a "
            "microphone",
            (unsigned long long)actual_channels,
            (unsigned)expected_channels
        );
        return kAudioDeviceUnsupportedFormatError;
    }
    return noErr;
}

static int amtui_tap_open_impl(
    amtui_tap **out,
    double *rate,
    uint32_t *channels,
    char *err,
    size_t errlen
) {
    amtui_tap *tap = NULL;
    CATapDescription *description = nil;
    CFStringRef output_uid = NULL;
    CFStringRef tap_uid = NULL;
    CFMutableDictionaryRef subdevice_entry = NULL;
    CFMutableDictionaryRef tap_entry = NULL;
    CFArrayRef subdevice_list = NULL;
    CFArrayRef tap_list = NULL;
    CFMutableDictionaryRef aggregate_properties = NULL;
    NSString *aggregate_name = nil;
    NSString *aggregate_uid = nil;
    OSStatus status = noErr;
    const char *operation = "amtui_tap_open";
    char detail[320] = {0};

    if (err != NULL && errlen != 0) {
        err[0] = '\0';
    }
    if (out == NULL || rate == NULL || channels == NULL) {
        amtui_set_error(
            err,
            errlen,
            operation,
            kAudioHardwareIllegalOperationError,
            "out, rate, and channels must be non-null"
        );
        return -1;
    }
    *out = NULL;
    *rate = 0.0;
    *channels = 0;

    tap = (amtui_tap *)calloc(1, sizeof(*tap));
    if (tap == NULL) {
        amtui_set_error(
            err,
            errlen,
            "allocate tap state",
            kAudioHardwareUnspecifiedError,
            "out of memory"
        );
        return -1;
    }
    tap->tap_id = kAudioObjectUnknown;
    tap->aggregate_id = kAudioObjectUnknown;

    AudioObjectPropertyAddress property = {
        .mSelector = kAudioHardwarePropertyDefaultOutputDevice,
        .mScope = kAudioObjectPropertyScopeGlobal,
        .mElement = kAudioObjectPropertyElementMain,
    };
    AudioObjectID output_device = kAudioObjectUnknown;
    UInt32 property_size = sizeof(output_device);
    operation = "resolve default output device";
    status = AudioObjectGetPropertyData(
        kAudioObjectSystemObject,
        &property,
        0,
        NULL,
        &property_size,
        &output_device
    );
    if (status != noErr) {
        goto fail;
    }
    if (output_device == kAudioObjectUnknown) {
        status = kAudioHardwareBadDeviceError;
        snprintf(detail, sizeof(detail), "CoreAudio returned no default output device");
        goto fail;
    }

    property.mSelector = kAudioDevicePropertyDeviceUID;
    property_size = sizeof(output_uid);
    operation = "read default output device UID";
    status = AudioObjectGetPropertyData(
        output_device,
        &property,
        0,
        NULL,
        &property_size,
        &output_uid
    );
    if (status != noErr) {
        goto fail;
    }
    if (output_uid == NULL) {
        status = kAudioHardwareUnspecifiedError;
        snprintf(detail, sizeof(detail), "CoreAudio returned a null device UID");
        goto fail;
    }

    description = [[CATapDescription alloc]
        initStereoGlobalTapButExcludeProcesses:@[]];
    operation = "create CATapDescription";
    if (description == nil) {
        status = kAudioHardwareUnspecifiedError;
        snprintf(detail, sizeof(detail), "Objective-C initializer returned nil");
        goto fail;
    }
    [description setMuteBehavior:CATapUnmuted];
    [description setPrivate:YES];
    [description setExclusive:YES];
    [description setName:@"amtui"];

    operation = "AudioHardwareCreateProcessTap";
    status = AudioHardwareCreateProcessTap(description, &tap->tap_id);
#if !__has_feature(objc_arc)
    [description release];
#endif
    description = nil;
    if (status != noErr) {
        goto fail;
    }

    property.mSelector = kAudioTapPropertyFormat;
    property_size = sizeof(tap->asbd);
    operation = "read kAudioTapPropertyFormat";
    status = AudioObjectGetPropertyData(
        tap->tap_id,
        &property,
        0,
        NULL,
        &property_size,
        &tap->asbd
    );
    if (status != noErr) {
        goto fail;
    }

    operation = "validate kAudioTapPropertyFormat";
    status = amtui_configure_format(
        tap,
        &tap->asbd,
        detail,
        sizeof(detail)
    );
    if (status != noErr) {
        goto fail;
    }

    property.mSelector = kAudioTapPropertyUID;
    property_size = sizeof(tap_uid);
    operation = "read kAudioTapPropertyUID";
    status = AudioObjectGetPropertyData(
        tap->tap_id,
        &property,
        0,
        NULL,
        &property_size,
        &tap_uid
    );
    if (status != noErr) {
        goto fail;
    }
    if (tap_uid == NULL) {
        status = kAudioHardwareUnspecifiedError;
        snprintf(detail, sizeof(detail), "CoreAudio returned a null tap UID");
        goto fail;
    }

    subdevice_entry = CFDictionaryCreateMutable(
        kCFAllocatorDefault,
        0,
        &kCFTypeDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks
    );
    tap_entry = CFDictionaryCreateMutable(
        kCFAllocatorDefault,
        0,
        &kCFTypeDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks
    );
    aggregate_properties = CFDictionaryCreateMutable(
        kCFAllocatorDefault,
        0,
        &kCFTypeDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks
    );
    operation = "allocate aggregate device dictionaries";
    if (subdevice_entry == NULL || tap_entry == NULL ||
        aggregate_properties == NULL) {
        status = kAudioHardwareUnspecifiedError;
        snprintf(detail, sizeof(detail), "out of memory");
        goto fail;
    }

    CFDictionarySetValue(
        subdevice_entry,
        CFSTR(kAudioSubDeviceUIDKey),
        output_uid
    );
    CFDictionarySetValue(
        subdevice_entry,
        CFSTR(kAudioSubDeviceDriftCompensationKey),
        kCFBooleanFalse
    );
    const void *subdevices[] = {subdevice_entry};
    subdevice_list = CFArrayCreate(
        kCFAllocatorDefault,
        subdevices,
        1,
        &kCFTypeArrayCallBacks
    );

    CFDictionarySetValue(tap_entry, CFSTR(kAudioSubTapUIDKey), tap_uid);
    CFDictionarySetValue(
        tap_entry,
        CFSTR(kAudioSubTapDriftCompensationKey),
        kCFBooleanTrue
    );
    const void *taps[] = {tap_entry};
    tap_list = CFArrayCreate(
        kCFAllocatorDefault,
        taps,
        1,
        &kCFTypeArrayCallBacks
    );
    operation = "allocate aggregate device lists";
    if (subdevice_list == NULL || tap_list == NULL) {
        status = kAudioHardwareUnspecifiedError;
        snprintf(detail, sizeof(detail), "out of memory");
        goto fail;
    }

    NSString *uuid = [[NSUUID UUID] UUIDString];
    aggregate_name = [NSString stringWithFormat:@"amtui-%@", uuid];
    aggregate_uid = [NSString stringWithFormat:@"com.applemusictui.amtui.%@", uuid];
    if (aggregate_name == nil || aggregate_uid == nil) {
        operation = "create aggregate device identity";
        status = kAudioHardwareUnspecifiedError;
        snprintf(detail, sizeof(detail), "Objective-C string creation failed");
        goto fail;
    }

    CFDictionarySetValue(
        aggregate_properties,
        CFSTR(kAudioAggregateDeviceNameKey),
        (CFStringRef)aggregate_name
    );
    CFDictionarySetValue(
        aggregate_properties,
        CFSTR(kAudioAggregateDeviceUIDKey),
        (CFStringRef)aggregate_uid
    );
    CFDictionarySetValue(
        aggregate_properties,
        CFSTR(kAudioAggregateDeviceMainSubDeviceKey),
        output_uid
    );
    CFDictionarySetValue(
        aggregate_properties,
        CFSTR(kAudioAggregateDeviceSubDeviceListKey),
        subdevice_list
    );
    CFDictionarySetValue(
        aggregate_properties,
        CFSTR(kAudioAggregateDeviceTapListKey),
        tap_list
    );
    CFDictionarySetValue(
        aggregate_properties,
        CFSTR(kAudioAggregateDeviceTapAutoStartKey),
        kCFBooleanTrue
    );
    CFDictionarySetValue(
        aggregate_properties,
        CFSTR(kAudioAggregateDeviceIsPrivateKey),
        kCFBooleanTrue
    );
    CFDictionarySetValue(
        aggregate_properties,
        CFSTR(kAudioAggregateDeviceIsStackedKey),
        kCFBooleanFalse
    );

    operation = "AudioHardwareCreateAggregateDevice";
    status = AudioHardwareCreateAggregateDevice(
        aggregate_properties,
        &tap->aggregate_id
    );
    if (status != noErr) {
        goto fail;
    }

    operation = "validate aggregate input stream configuration";
    status = amtui_validate_aggregate_input(
        tap->aggregate_id,
        tap->output_channels,
        detail,
        sizeof(detail)
    );
    if (status != noErr) {
        goto fail;
    }

    double capacity_samples = ceil(tap->asbd.mSampleRate) * 2.0;
    operation = "allocate audio ring";
    if (!isfinite(capacity_samples) || capacity_samples < 2.0 ||
        capacity_samples > (double)(SIZE_MAX / sizeof(float))) {
        status = kAudioHardwareIllegalOperationError;
        snprintf(detail, sizeof(detail), "sample rate produces an invalid ring size");
        goto fail;
    }
    tap->ring_capacity = (size_t)capacity_samples;
    tap->ring = (float *)calloc(tap->ring_capacity, sizeof(float));
    if (tap->ring == NULL) {
        status = kAudioHardwareUnspecifiedError;
        snprintf(detail, sizeof(detail), "out of memory");
        goto fail;
    }
    atomic_init(&tap->write_index, 0);
    atomic_init(&tap->read_index, 0);
    if (!atomic_is_lock_free(&tap->write_index) ||
        !atomic_is_lock_free(&tap->read_index)) {
        status = kAudioHardwareUnsupportedOperationError;
        snprintf(detail, sizeof(detail), "64-bit ring atomics are not lock-free");
        goto fail;
    }

    operation = "AudioDeviceCreateIOProcID";
    status = AudioDeviceCreateIOProcID(
        tap->aggregate_id,
        amtui_io_proc,
        tap,
        &tap->io_proc_id
    );
    if (status != noErr) {
        goto fail;
    }

    operation = "AudioDeviceStart";
    status = AudioDeviceStart(tap->aggregate_id, tap->io_proc_id);
    if (status != noErr) {
        goto fail;
    }
    tap->started = true;

    if (aggregate_properties != NULL) {
        CFRelease(aggregate_properties);
    }
    if (tap_list != NULL) {
        CFRelease(tap_list);
    }
    if (subdevice_list != NULL) {
        CFRelease(subdevice_list);
    }
    if (tap_entry != NULL) {
        CFRelease(tap_entry);
    }
    if (subdevice_entry != NULL) {
        CFRelease(subdevice_entry);
    }
    if (tap_uid != NULL) {
        CFRelease(tap_uid);
    }
    if (output_uid != NULL) {
        CFRelease(output_uid);
    }

    *rate = tap->asbd.mSampleRate;
    *channels = tap->output_channels;
    *out = tap;
    return 0;

fail:
    amtui_set_error(err, errlen, operation, status, detail);
#if !__has_feature(objc_arc)
    if (description != nil) {
        [description release];
    }
#endif
    if (aggregate_properties != NULL) {
        CFRelease(aggregate_properties);
    }
    if (tap_list != NULL) {
        CFRelease(tap_list);
    }
    if (subdevice_list != NULL) {
        CFRelease(subdevice_list);
    }
    if (tap_entry != NULL) {
        CFRelease(tap_entry);
    }
    if (subdevice_entry != NULL) {
        CFRelease(subdevice_entry);
    }
    if (tap_uid != NULL) {
        CFRelease(tap_uid);
    }
    if (output_uid != NULL) {
        CFRelease(output_uid);
    }
    amtui_tap_close(tap);
    return -1;
}

int amtui_tap_open(
    amtui_tap **out,
    double *rate,
    uint32_t *channels,
    char *err,
    size_t errlen
) {
    @autoreleasepool {
        return amtui_tap_open_impl(out, rate, channels, err, errlen);
    }
}

int amtui_tap_read(amtui_tap *tap, float *dst, int maxSamples) {
    return amtui_ring_read_samples(tap, dst, maxSamples);
}

typedef struct {
    UInt32 mNumberBuffers;
    AudioBuffer mBuffers[2];
} amtui_two_buffer_list;

int amtui_internal_convert_push(
    uint32_t sampleKind,
    int nonInterleaved,
    uint32_t channels,
    const void *buffer0,
    size_t buffer0Bytes,
    const void *buffer1,
    size_t buffer1Bytes,
    float *dst,
    int dstCapacity
) {
    if (channels == 0 || channels > 2 || buffer0 == NULL ||
        dst == NULL || dstCapacity <= 0 ||
        buffer0Bytes > UINT32_MAX || buffer1Bytes > UINT32_MAX ||
        (nonInterleaved && channels == 2 && buffer1 == NULL)) {
        return -1;
    }

    size_t bytes_per_sample = 0;
    amtui_sample_kind kind;
    switch (sampleKind) {
        case AMTUI_INTERNAL_SAMPLE_FLOAT32:
            kind = AMTUI_SAMPLE_FLOAT32;
            bytes_per_sample = sizeof(float);
            break;
        case AMTUI_INTERNAL_SAMPLE_FLOAT64:
            kind = AMTUI_SAMPLE_FLOAT64;
            bytes_per_sample = sizeof(double);
            break;
        case AMTUI_INTERNAL_SAMPLE_PCM16:
            kind = AMTUI_SAMPLE_PCM16;
            bytes_per_sample = sizeof(int16_t);
            break;
        default:
            return -1;
    }

    amtui_tap tap;
    memset(&tap, 0, sizeof(tap));
    tap.sample_kind = kind;
    tap.input_channels = channels;
    tap.output_channels = channels;
    tap.bytes_per_sample = bytes_per_sample;
    tap.non_interleaved = nonInterleaved != 0;
    tap.asbd.mChannelsPerFrame = channels;
    tap.asbd.mBytesPerFrame = (UInt32)(
        bytes_per_sample * (tap.non_interleaved ? 1 : channels)
    );
    tap.ring = dst;
    tap.ring_capacity = (size_t)dstCapacity;
    atomic_init(&tap.write_index, 0);
    atomic_init(&tap.read_index, 0);

    amtui_two_buffer_list buffers;
    memset(&buffers, 0, sizeof(buffers));
    if (tap.non_interleaved) {
        buffers.mNumberBuffers = channels;
        buffers.mBuffers[0].mNumberChannels = 1;
        buffers.mBuffers[0].mDataByteSize = (UInt32)buffer0Bytes;
        buffers.mBuffers[0].mData = (void *)buffer0;
        if (channels == 2) {
            buffers.mBuffers[1].mNumberChannels = 1;
            buffers.mBuffers[1].mDataByteSize = (UInt32)buffer1Bytes;
            buffers.mBuffers[1].mData = (void *)buffer1;
        }
    } else {
        buffers.mNumberBuffers = 1;
        buffers.mBuffers[0].mNumberChannels = channels;
        buffers.mBuffers[0].mDataByteSize = (UInt32)buffer0Bytes;
        buffers.mBuffers[0].mData = (void *)buffer0;
    }

    size_t count = amtui_convert_and_push(
        &tap,
        (const AudioBufferList *)&buffers
    );
    return count > INT_MAX ? -1 : (int)count;
}

int amtui_internal_ring_push(
    float *ring,
    int capacity,
    uint64_t *readIndex,
    uint64_t *writeIndex,
    const float *src,
    int sampleCount
) {
    if (ring == NULL || capacity <= 0 || readIndex == NULL ||
        writeIndex == NULL || sampleCount < 0 ||
        (sampleCount > 0 && src == NULL)) {
        return -1;
    }

    amtui_tap tap;
    memset(&tap, 0, sizeof(tap));
    tap.ring = ring;
    tap.ring_capacity = (size_t)capacity;
    atomic_init(&tap.read_index, *readIndex);
    atomic_init(&tap.write_index, *writeIndex);

    bool pushed = amtui_ring_push_samples(
        &tap,
        src,
        (size_t)sampleCount
    );
    *readIndex =
        atomic_load_explicit(&tap.read_index, memory_order_relaxed);
    *writeIndex =
        atomic_load_explicit(&tap.write_index, memory_order_relaxed);
    return pushed ? 1 : 0;
}

int amtui_internal_ring_read(
    float *ring,
    int capacity,
    uint64_t *readIndex,
    uint64_t *writeIndex,
    float *dst,
    int maxSamples
) {
    if (ring == NULL || capacity <= 0 || readIndex == NULL ||
        writeIndex == NULL || dst == NULL || maxSamples <= 0) {
        return -1;
    }

    amtui_tap tap;
    memset(&tap, 0, sizeof(tap));
    tap.ring = ring;
    tap.ring_capacity = (size_t)capacity;
    atomic_init(&tap.read_index, *readIndex);
    atomic_init(&tap.write_index, *writeIndex);

    int count = amtui_ring_read_samples(&tap, dst, maxSamples);
    *readIndex =
        atomic_load_explicit(&tap.read_index, memory_order_relaxed);
    *writeIndex =
        atomic_load_explicit(&tap.write_index, memory_order_relaxed);
    return count;
}

typedef struct {
    amtui_tap tap;
    uint32_t sample_count;
    _Atomic int failure;
} amtui_ring_stress_context;

static void *amtui_ring_stress_producer(void *opaque) {
    amtui_ring_stress_context *context =
        (amtui_ring_stress_context *)opaque;
    uint32_t next = 1;
    float chunk[7];

    while (next <= context->sample_count) {
        if (atomic_load_explicit(
                &context->failure,
                memory_order_acquire
            ) != 0) {
            return NULL;
        }

        size_t count = (size_t)((next % 7u) + 1u);
        size_t remaining =
            (size_t)context->sample_count - (size_t)next + 1u;
        if (count > remaining) {
            count = remaining;
        }
        for (size_t index = 0; index < count; index++) {
            chunk[index] = (float)(next + (uint32_t)index);
        }
        if (amtui_ring_push_samples(&context->tap, chunk, count)) {
            next += (uint32_t)count;
        }
    }
    return NULL;
}

static void *amtui_ring_stress_consumer(void *opaque) {
    amtui_ring_stress_context *context =
        (amtui_ring_stress_context *)opaque;
    uint32_t expected = 1;
    float chunk[11];

    while (expected <= context->sample_count) {
        if (atomic_load_explicit(
                &context->failure,
                memory_order_acquire
            ) != 0) {
            return NULL;
        }

        int max_samples = (int)((expected % 11u) + 1u);
        int count = amtui_ring_read_samples(
            &context->tap,
            chunk,
            max_samples
        );
        for (int index = 0; index < count; index++) {
            if (chunk[index] != (float)expected) {
                atomic_store_explicit(
                    &context->failure,
                    1,
                    memory_order_release
                );
                return NULL;
            }
            expected++;
        }
    }
    return NULL;
}

int amtui_internal_ring_spsc_stress(
    uint32_t sampleCount,
    uint32_t capacity
) {
    if (sampleCount == 0 || sampleCount > 1000000u || capacity < 8u) {
        return -1;
    }

    amtui_ring_stress_context context;
    memset(&context, 0, sizeof(context));
    context.sample_count = sampleCount;
    context.tap.ring_capacity = capacity;
    context.tap.ring = (float *)calloc(capacity, sizeof(float));
    if (context.tap.ring == NULL) {
        return -2;
    }
    atomic_init(&context.tap.read_index, 0);
    atomic_init(&context.tap.write_index, 0);
    atomic_init(&context.failure, 0);
    if (!atomic_is_lock_free(&context.tap.read_index) ||
        !atomic_is_lock_free(&context.tap.write_index)) {
        free(context.tap.ring);
        return -8;
    }

    pthread_t producer;
    pthread_t consumer;
    int create_status = pthread_create(
        &producer,
        NULL,
        amtui_ring_stress_producer,
        &context
    );
    if (create_status != 0) {
        free(context.tap.ring);
        return -3;
    }
    create_status = pthread_create(
        &consumer,
        NULL,
        amtui_ring_stress_consumer,
        &context
    );
    if (create_status != 0) {
        atomic_store_explicit(&context.failure, 1, memory_order_release);
        pthread_join(producer, NULL);
        free(context.tap.ring);
        return -4;
    }

    int producer_join = pthread_join(producer, NULL);
    int consumer_join = pthread_join(consumer, NULL);
    int failure =
        atomic_load_explicit(&context.failure, memory_order_acquire);
    uint64_t read =
        atomic_load_explicit(&context.tap.read_index, memory_order_acquire);
    uint64_t write =
        atomic_load_explicit(&context.tap.write_index, memory_order_acquire);
    free(context.tap.ring);

    if (producer_join != 0 || consumer_join != 0) {
        return -5;
    }
    if (failure != 0) {
        return -6;
    }
    if (read != sampleCount || write != sampleCount) {
        return -7;
    }
    return 0;
}

static bool amtui_teardown_can_release(
    OSStatus stop_status,
    OSStatus destroy_io_proc_status
) {
    return stop_status == noErr && destroy_io_proc_status == noErr;
}

int amtui_internal_teardown_can_release(
    int32_t stopStatus,
    int32_t destroyIOProcStatus
) {
    return amtui_teardown_can_release(
        (OSStatus)stopStatus,
        (OSStatus)destroyIOProcStatus
    ) ? 1 : 0;
}

int32_t amtui_tap_close(amtui_tap *tap) {
    if (tap == NULL) {
        return noErr;
    }

    if (tap->started) {
        OSStatus stop_status =
            AudioDeviceStop(tap->aggregate_id, tap->io_proc_id);
        if (!amtui_teardown_can_release(stop_status, noErr)) {
            return stop_status;
        }
        tap->started = false;
    }

    if (tap->io_proc_id != NULL) {
        OSStatus destroy_status = AudioDeviceDestroyIOProcID(
            tap->aggregate_id,
            tap->io_proc_id
        );
        if (!amtui_teardown_can_release(noErr, destroy_status)) {
            return destroy_status;
        }
        tap->io_proc_id = NULL;
    }

    OSStatus result = noErr;
    if (tap->aggregate_id != kAudioObjectUnknown) {
        OSStatus status =
            AudioHardwareDestroyAggregateDevice(tap->aggregate_id);
        if (result == noErr && status != noErr) {
            result = status;
        }
        tap->aggregate_id = kAudioObjectUnknown;
    }
    if (tap->tap_id != kAudioObjectUnknown) {
        OSStatus status = AudioHardwareDestroyProcessTap(tap->tap_id);
        if (result == noErr && status != noErr) {
            result = status;
        }
        tap->tap_id = kAudioObjectUnknown;
    }
    free(tap->ring);
    tap->ring = NULL;
    free(tap);
    return result;
}
