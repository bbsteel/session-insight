package main

import "testing"

func TestResolveStatus(t *testing.T) {
	cases := []struct {
		name         string
		state        *ItemState
		hasCandidate bool
		blobExists   bool
		lock         string
		want         ItemStatus
	}{
		{"nothing at all", nil, false, false, "", ItemStatus{Status: StatusMissing}},
		{"candidate only", nil, true, false, "", ItemStatus{Status: StatusFound}},
		{"empty record with candidate", &ItemState{}, true, false, "", ItemStatus{Status: StatusFound}},
		{"not applicable wins over candidate", &ItemState{NotApplicable: true, NotApplicableReason: "x"}, true, false, "", ItemStatus{Status: StatusNotApplicable}},
		{"captured", &ItemState{Current: &CaptureRecord{Hash: "a"}}, false, true, "", ItemStatus{Status: StatusCaptured}},
		{"legacy accept is not used", &ItemState{Current: &CaptureRecord{Hash: "a"}, LegacyAcceptedHash: "a"}, false, true, "", ItemStatus{Status: StatusCaptured}},
		{"used from main lock", &ItemState{Current: &CaptureRecord{Hash: "a"}}, false, true, "a", ItemStatus{Status: StatusUsed}},
		{"update available", &ItemState{Current: &CaptureRecord{Hash: "b"}}, false, true, "a", ItemStatus{Status: StatusUpdateAvailable}},
		{"lock match but blob gone", &ItemState{Current: &CaptureRecord{Hash: "a"}}, false, false, "a", ItemStatus{Status: StatusUsed, LocalUnavailable: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveStatus(tc.state, tc.hasCandidate, tc.blobExists, tc.lock)
			if got != tc.want {
				t.Fatalf("resolveStatus = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestMigrateLegacyAcceptedHash(t *testing.T) {
	st := &ItemState{AcceptedHash: "abc", AcceptedExt: ".png"}
	migrateItemState(st)
	if st.LegacyAcceptedHash != "abc" || st.LegacyAcceptedExt != ".png" {
		t.Fatalf("legacy not preserved: %+v", st)
	}
	if st.AcceptedHash != "" || st.AcceptedExt != "" {
		t.Fatalf("accepted hash must be cleared: %+v", st)
	}
}
