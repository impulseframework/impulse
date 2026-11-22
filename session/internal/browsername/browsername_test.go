package browsername

import "testing"

func TestOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		userAgent string
		want      string
	}{
		{
			name:      "Chrome on Linux",
			userAgent: "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
			want:      "Chrome on Linux",
		},
		{
			name:      "Chrome on Android, whose header names Linux too",
			userAgent: "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36",
			want:      "Chrome on Android",
		},
		{
			name:      "Chrome on Windows",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
			want:      "Chrome on Windows",
		},
		{
			name:      "Chrome on macOS",
			userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
			want:      "Chrome on macOS",
		},
		{
			name:      "Chrome on ChromeOS, whose header names X11 too",
			userAgent: "Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
			want:      "Chrome on ChromeOS",
		},
		{
			name:      "Chrome on iPhone",
			userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/131.0.6778.73 Mobile/15E148 Safari/604.1",
			want:      "Chrome on iPhone",
		},
		{
			name:      "Firefox on Windows",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0",
			want:      "Firefox on Windows",
		},
		{
			name:      "Firefox on iPhone",
			userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/133.0 Mobile/15E148 Safari/605.1.15",
			want:      "Firefox on iPhone",
		},
		{
			name:      "Safari on macOS",
			userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15",
			want:      "Safari on macOS",
		},
		{
			name:      "Safari on iPhone, whose header says like Mac OS X",
			userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1",
			want:      "Safari on iPhone",
		},
		{
			name:      "Safari on iPad",
			userAgent: "Mozilla/5.0 (iPad; CPU OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1",
			want:      "Safari on iPad",
		},
		{
			name:      "Edge on Windows, whose header names Chrome too",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0",
			want:      "Edge on Windows",
		},
		{
			name:      "Opera on Windows, whose header names Chrome too",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 OPR/116.0.0.0",
			want:      "Opera on Windows",
		},
		{
			name:      "Samsung Internet on Android",
			userAgent: "Mozilla/5.0 (Linux; Android 14; SAMSUNG SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/27.0 Chrome/125.0.0.0 Mobile Safari/537.36",
			want:      "Samsung Internet on Android",
		},
		{
			name:      "a command-line client names neither",
			userAgent: "curl/8.5.0",
			want:      "",
		},
		{
			name:      "no header names neither",
			userAgent: "",
			want:      "",
		},
		{
			name:      "a system with no browser it names is the system alone",
			userAgent: "Dalvik/2.1.0 (Linux; U; Android 14; Pixel 8 Build/AP2A.240805.005)",
			want:      "Android",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Of(tt.userAgent); got != tt.want {
				t.Errorf("Of() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPhone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want bool
	}{
		{name: "Chrome on Android", want: true},
		{name: "Safari on iPhone", want: true},
		{name: "Android", want: true},
		{name: "Safari on iPad", want: false},
		{name: "Chrome on Linux", want: false},
		{name: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Phone(tt.name); got != tt.want {
				t.Errorf("Phone(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
