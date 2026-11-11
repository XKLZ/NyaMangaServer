package provider

import "testing"

func TestRemoteFileInfoExactOfflineValues(t *testing.T) {
	p, b, e := ParseRemoteFileInfo([]byte(`{"gid":123,"filecount":"987","filesize":9007199254740993}`), "123")
	if e != nil || p == nil || b == nil || *p != 987 || *b != 9007199254740993 {
		t.Fatal(p, b, e)
	}
	p, b, e = ParseRemoteFileInfo([]byte(`{"gid":"123"}`), "123")
	if e != nil || p != nil || b != nil {
		t.Fatal(p, b, e)
	}
	for _, raw := range []string{`{"gid":124,"filecount":2}`, `{"gid":123,"filecount":-1}`, `{"gid":123,"filesize":"1.5 MB"}`, `{"gid":123,"filesize":1.2}`, `{"gid":123,"filesize":"9223372036854775808"}`} {
		if _, _, e := ParseRemoteFileInfo([]byte(raw), "123"); e == nil {
			t.Fatal("accepted invalid info", raw)
		}
	}
}
func TestRemoteFileInfoProviderCanonicalAndRawRemainExact(t *testing.T) {
	c := New(vaultForTest(t))
	result, e := c.parseGallery([]byte(`{"gid":123,"token":"0123456789","filecount":"12","filesize":9007199254740993,"tags":[]}`), "https://e-hentai.org/g/123/0123456789/", "123", "0123456789")
	if e != nil || result.Fields.RemoteSizeBytes == nil || *result.Fields.RemoteSizeBytes != 9007199254740993 {
		t.Fatal("canonical number rounded", result, e)
	}
	_, size, e := ParseRemoteFileInfo(result.Raw, "123")
	if e != nil || size == nil || *size != 9007199254740993 {
		t.Fatal("raw number rounded", size, e)
	}
}
