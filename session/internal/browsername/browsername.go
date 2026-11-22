// Package browsername names a browser from its User-Agent header, in words a person tells
// their browsers apart by.
package browsername

import "strings"

// mark is a word Of writes and the substrings of a User-Agent header that call for it.
// all says every substring must be in the header; otherwise any one of them does.
type mark struct {
	word       string
	substrings []string
	all        bool
}

// in reports whether userAgent holds what m looks for.
func (m mark) in(userAgent string) bool {
	for _, s := range m.substrings {
		found := strings.Contains(userAgent, s)
		if found && !m.all {
			return true
		}
		if !found && m.all {
			return false
		}
	}

	return m.all
}

// browsers are tried in order: a browser built on another names that one in its header
// too, so it comes before it (Edge and Opera before Chrome, Chrome before Safari).
var browsers = []mark{
	{word: "Edge", substrings: []string{"Edg/", "EdgA/", "EdgiOS/"}},
	{word: "Opera", substrings: []string{"OPR/", "Opera"}},
	{word: "Samsung Internet", substrings: []string{"SamsungBrowser/"}},
	{word: "Firefox", substrings: []string{"Firefox/", "FxiOS/"}},
	{word: "Chrome", substrings: []string{"Chrome/", "CriOS/", "Chromium/"}},
	{word: "Safari", substrings: []string{"Safari/", "Version/"}, all: true},
}

// The words of the phones' systems, which Phone looks for too.
const (
	android = "Android"
	iPhone  = "iPhone"
)

// systems are tried in order: Android's header names Linux, an iPhone's says "like Mac
// OS X" and ChromeOS's names X11, so each comes before the system it also names.
var systems = []mark{
	{word: android, substrings: []string{android}},
	{word: iPhone, substrings: []string{iPhone, "iPod"}},
	{word: "iPad", substrings: []string{"iPad"}},
	{word: "ChromeOS", substrings: []string{"CrOS"}},
	{word: "Windows", substrings: []string{"Windows"}},
	{word: "macOS", substrings: []string{"Macintosh", "Mac OS X"}},
	{word: "Linux", substrings: []string{"Linux", "X11"}},
}

// phones are the systems whose browsers are phones'.
var phones = []string{android, iPhone}

// Of names the browser that sent userAgent, its User-Agent header: "Chrome on Android"
// where both the browser and its system are recognized, the one word where only one of
// them is, and "" where neither is. The name is made of the fixed words above alone, so
// nothing the header holds reaches it.
func Of(userAgent string) string {
	browser := first(browsers, userAgent)
	system := first(systems, userAgent)
	switch {
	case browser != "" && system != "":
		return browser + " on " + system
	case browser != "":
		return browser
	default:
		return system
	}
}

// Phone reports whether name, as Of writes it, is a phone's browser: it is, or ends in,
// the name of a phone's system.
func Phone(name string) bool {
	for _, system := range phones {
		if strings.HasSuffix(name, system) {
			return true
		}
	}

	return false
}

// first returns the word of the first mark of marks that userAgent holds, "" for none.
func first(marks []mark, userAgent string) string {
	for _, m := range marks {
		if m.in(userAgent) {
			return m.word
		}
	}

	return ""
}
