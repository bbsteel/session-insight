package main

import "testing"

func TestResolveStatus(t *testing.T) {
	cases := []struct {
		name         string
		state        *ItemState
		hasCandidate bool
		blobExists   bool
		want         ItemStatus
	}{
		{"nothing at all", nil, false, false, ItemStatus{Status: StatusMissing}},
		{"candidate only", nil, true, false, ItemStatus{Status: StatusFound}},
		{"empty record with candidate", &ItemState{}, true, false, ItemStatus{Status: StatusFound}},
		{"not applicable wins over candidate", &ItemState{NotApplicable: true, NotApplicableReason: "x"}, true, false, ItemStatus{Status: StatusNotApplicable}},
		{"captured", &ItemState{Current: &CaptureRecord{Hash: "a"}}, false, true, ItemStatus{Status: StatusCaptured}},
		{"used", &ItemState{Current: &CaptureRecord{Hash: "a"}, AcceptedHash: "a"}, false, true, ItemStatus{Status: StatusUsed}},
		{"update available", &ItemState{Current: &CaptureRecord{Hash: "b"}, AcceptedHash: "a"}, false, true, ItemStatus{Status: StatusUpdateAvailable}},
		{"accepted but blob gone", &ItemState{Current: &CaptureRecord{Hash: "a"}, AcceptedHash: "a"}, false, false, ItemStatus{Status: StatusUsed, LocalUnavailable: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveStatus(tc.state, tc.hasCandidate, tc.blobExists)
			if got != tc.want {
				t.Fatalf("resolveStatus = %+v, want %+v", got, tc.want)
			}
		})
	}
}
