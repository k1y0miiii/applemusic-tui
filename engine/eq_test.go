package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestEQPreampPullsDownByTheLoudestBoost(t *testing.T) {
	cases := []struct {
		gains [10]int
		want  float64
	}{
		{[10]int{}, 0},
		{[10]int{6, 5, 4, 2, 0, 0, 0, 0, 0, 0}, -6},
		{[10]int{-2, -2, -1, 1, 3, 4, 3, 1, 0, -1}, -4},
		{[10]int{-12, -12, -12, -12, -12, -12, -12, -12, -12, -12}, 0}, // no boost, no preamp
	}
	for _, c := range cases {
		if got := eqPreampDB(c.gains); got != c.want {
			t.Errorf("eqPreampDB(%v) = %v, want %v", c.gains, got, c.want)
		}
	}
}

func TestEQJSFormatsCleanly(t *testing.T) {
	gains := [10]int{6, 5, 4, 2, 0, 0, 0, 0, 0, 0}
	b, err := json.Marshal(gains)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := fmt.Sprintf(eqJS, string(b), eqPreampDB(gains))
	if strings.Contains(js, "%!") {
		t.Fatalf("eqJS formatting left a Go fmt error marker: %s", js)
	}
	if !strings.Contains(js, "[6,5,4,2,0,0,0,0,0,0]") {
		t.Fatalf("eqJS did not embed the gains array: %s", js)
	}
	if !strings.Contains(js, "-6") {
		t.Fatalf("eqJS did not embed the preamp: %s", js)
	}
}
