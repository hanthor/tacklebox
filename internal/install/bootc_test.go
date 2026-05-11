package install

import "testing"

func TestParseBackend(t *testing.T) {
	cases := []struct {
		name string
		json string
		want Backend
	}{
		{"ostree label", `{"Labels":{"ostree.bootable":"true"}}`, BackendOstree},
		{"empty inspect falls back to composefs", `{"Labels":{}}`, BackendComposefs},
		{"composefs only", `{"Annotations":{"composefs.digest":"abc"}}`, BackendComposefs},
		{"ostree substring anywhere wins", `{"Comment":"based on ostree pipeline"}`, BackendOstree},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseBackend(tc.json); got != tc.want {
				t.Errorf("parseBackend(%q) = %q, want %q", tc.json, got, tc.want)
			}
		})
	}
}
