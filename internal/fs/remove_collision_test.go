package fs

import (
	"reflect"
	"testing"
)

func TestRemoveTargetsFileDirectoryCollisions(t *testing.T) {
	sb := &Superblock{Entries: make(map[string]map[string]interface{})}
	for _, name := range []string{"a", "a/child", "a/.hidden", "parent/a", "parent/a/child", "plain/child", "link/child", "link-only", "other"} {
		sb.Entries[name] = map[string]interface{}{"v1": &RegularFile{Path: name}}
	}
	sb.Entries["link"] = map[string]interface{}{"v1": &Symlink{Path: "link", Target: "plain"}}
	sb.Entries["link-only"] = map[string]interface{}{"v1": &Symlink{Path: "link-only", Target: "plain"}}
	for _, tc := range []struct {
		patterns []string
		want     []string
	}{
		{[]string{"a"}, []string{"a"}},
		{[]string{"./a", "a"}, []string{"a"}},
		{[]string{"a/"}, []string{"a/.hidden", "a/child"}},
		{[]string{"./a//"}, []string{"a/.hidden", "a/child"}},
		{[]string{"a/", "a", "a/child"}, []string{"a", "a/.hidden", "a/child"}},
		{[]string{"parent/a"}, []string{"parent/a"}},
		{[]string{"parent/a/"}, []string{"parent/a/child"}},
		{[]string{"parent"}, []string{"parent/a", "parent/a/child"}},
		{[]string{"plain"}, []string{"plain/child"}},
		{[]string{"a*"}, []string{"a"}},
		{[]string{"a", "a/*"}, []string{"a", "a/.hidden", "a/child"}},
		{[]string{"link"}, []string{"link"}},
		{[]string{"link/"}, []string{"link/child"}},
		{[]string{"link-only/"}, []string{}},
		{[]string{"other/"}, []string{}},
		{[]string{"missing"}, []string{}},
	} {
		got, err := sb.RemoveTargets(tc.patterns)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("RemoveTargets(%v) = %v, %v; want %v", tc.patterns, got, err, tc.want)
		}
	}
	if _, err := sb.RemoveTargets(nil); err == nil {
		t.Fatal("accepted empty removal patterns")
	}
	// Once the last file version is gone, a bare name selects the directory.
	delete(sb.Entries, "a")
	if got, err := sb.RemoveTargets([]string{"a"}); err != nil || !reflect.DeepEqual(got, []string{"a/.hidden", "a/child"}) {
		t.Fatalf("directory fallback: %v, %v", got, err)
	}
}
