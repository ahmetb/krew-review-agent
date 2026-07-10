package tools

import "testing"

func TestValidatePluginName(t *testing.T) {
	valid := []string{
		"whoami", "rbac-lookup", "node-admin", "a", "abc123", "k1", "foo-bar-baz",
	}
	for _, n := range valid {
		if err := ValidatePluginName(n); err != nil {
			t.Errorf("ValidatePluginName(%q)=%v want nil", n, err)
		}
	}
	invalid := []string{
		"",                                  // empty
		"Kube-foo",                          // uppercase
		"foo_bar",                           // underscore
		"foo/bar",                           // path separator
		"..",                                // traversal
		"foo..bar",                          // double dot
		"foo bar",                           // space
		"-foo",                              // leading hyphen
		"foo-",                              // trailing hyphen
		"foo--bar",                          // double hyphen
		"Whoami",                            // uppercase
		"foo.yaml",                          // dot
	}
	for _, n := range invalid {
		if err := ValidatePluginName(n); err == nil {
			t.Errorf("ValidatePluginName(%q)=nil want error", n)
		}
	}
}

func TestValidatePluginNameTooLong(t *testing.T) {
	long := make([]byte, maxPluginNameLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := ValidatePluginName(string(long)); err == nil {
		t.Error("expected error for overlong name")
	}
}
