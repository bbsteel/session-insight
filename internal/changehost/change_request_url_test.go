package changehost

import "testing"

func TestChangeRequestPathIdentityRecognizesHostedReviewShapes(t *testing.T) {
	cases := []struct {
		path, slug, number string
	}{
		{"/acme/widgets/pull/42", "acme/widgets", "42"},
		{"/acme/widgets/pulls/12", "acme/widgets", "12"},
		{"/group/project/-/merge_requests/7", "group/project", "7"},
		{"/group/project/merge_requests/7", "group/project", "7"},
		{"/ws/repo/pull-requests/3", "ws/repo", "3"},
		{"/org/proj/_git/repo/pullrequest/9", "org/proj/_git/repo", "9"},
		{"/team/repo/reviews/7", "team/repo", "7"},
		{"/c/platform/docs/+/81", "platform/docs", "81"},
	}
	for _, test := range cases {
		slug, number, ok := changeRequestPathIdentity(test.path)
		if !ok || slug != test.slug || number != test.number {
			t.Fatalf("path %q: slug=%q number=%q ok=%v", test.path, slug, number, ok)
		}
	}
}

func TestChangeRequestPathIdentityRejectsNonReviewPaths(t *testing.T) {
	for _, path := range []string{
		"/",
		"/acme/widgets",
		"/acme/widgets/issues/12",
		"/acme/widgets/pull",
		"/acme/widgets/pull/0",
		"/acme/widgets/pull/01",
		"/pull/42",
		"/about",
		"/c/+/81",
	} {
		if slug, number, ok := changeRequestPathIdentity(path); ok {
			t.Fatalf("accepted non-review path %q as %s#%s", path, slug, number)
		}
	}
}
