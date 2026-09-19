package util

import "testing"

func TestParseCookieHeaderWithSemicolons(t *testing.T) {
	headers := ParseHeaders([]string{"Cookie=session=abc; other=def"}, "")
	if got := headers["Cookie"]; got != "session=abc; other=def" {
		t.Fatalf("Cookie = %q", got)
	}
}

func TestParseEnvHeadersWithDoublePipe(t *testing.T) {
	headers := ParseHeaders(nil, "Cookie=session=abc; other=def||X-Api-Key=zzz")
	if headers["Cookie"] == "" || headers["X-Api-Key"] != "zzz" {
		t.Fatalf("headers = %#v", headers)
	}
}

func TestToSimplified(t *testing.T) {
	if got, want := ToSimplified("滿院落葉 後來"), "满院落叶 后来"; got != want {
		t.Fatalf("ToSimplified() = %q, want %q", got, want)
	}
}
