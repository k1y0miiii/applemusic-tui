#ifndef AMTUI_COREAUDIO_DARWIN_H
#define AMTUI_COREAUDIO_DARWIN_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct amtui_tap amtui_tap;

int amtui_tap_open(
    amtui_tap **out,
    double *rate,
    uint32_t *channels,
    char *err,
    size_t errlen
);

int amtui_tap_read(amtui_tap *tap, float *dst, int maxSamples);

int32_t amtui_tap_close(amtui_tap *tap);

/*
 * Internal, deterministic test bridge. These functions exercise the same
 * conversion and ring primitives as the IOProc without creating a process tap.
 */
enum {
    AMTUI_INTERNAL_SAMPLE_FLOAT32 = 1,
    AMTUI_INTERNAL_SAMPLE_FLOAT64 = 2,
    AMTUI_INTERNAL_SAMPLE_PCM16 = 3,
};

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
);

int amtui_internal_ring_push(
    float *ring,
    int capacity,
    uint64_t *readIndex,
    uint64_t *writeIndex,
    const float *src,
    int sampleCount
);

int amtui_internal_ring_read(
    float *ring,
    int capacity,
    uint64_t *readIndex,
    uint64_t *writeIndex,
    float *dst,
    int maxSamples
);

int amtui_internal_teardown_can_release(
    int32_t stopStatus,
    int32_t destroyIOProcStatus
);

int amtui_internal_ring_spsc_stress(
    uint32_t sampleCount,
    uint32_t capacity
);

#ifdef __cplusplus
}
#endif

#endif
