package dnsfilter

import (
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"time"

	"bufio"
	"fmt"
	"os"
	"runtime"

	"go.uber.org/goleak"
)

func TestRuleToRegexp(t *testing.T) {
	tests := []struct {
		rule   string
		result string
		err    error
	}{
		{"/doubleclick/", "doubleclick", nil},
		{"/", "", ErrInvalidSyntax},
		{`|double*?.+[]|(){}#$\|`, `^double.*\?\.\+\[\]\|\(\)\{\}\#\$\\$`, nil},
		{`||doubleclick.net^`, `^([a-z0-9-_.]+\.)?doubleclick\.net([^ a-zA-Z0-9.%]|$)`, nil},
	}
	for _, testcase := range tests {
		converted, err := ruleToRegexp(testcase.rule)
		if err != testcase.err {
			t.Error("Errors do not match, got ", err, " expected ", testcase.err)
		}
		if converted != testcase.result {
			t.Error("Results do not match, got ", converted, " expected ", testcase.result)
		}
	}
}

//
// helper functions
//
func (d *Dnsfilter) checkAddRule(t *testing.T, rule string) {
	t.Helper()
	err := d.AddRule(rule, 0)
	if err == nil {
		// nothing to report
		return
	}
	if err == ErrInvalidSyntax {
		t.Errorf("This rule has invalid syntax: %s", rule)
	}
	if err != nil {
		t.Errorf("Error while adding rule %s: %s", rule, err)
	}
}

func (d *Dnsfilter) checkAddRuleFail(t *testing.T, rule string) {
	t.Helper()
	err := d.AddRule(rule, 0)
	if err == ErrInvalidSyntax {
		return
	}
	if err != nil {
		t.Errorf("Error while adding rule %s: %s", rule, err)
	}
	t.Errorf("Adding this rule should have failed: %s", rule)
}

func (d *Dnsfilter) checkMatch(t *testing.T, hostname string) {
	t.Helper()
	ret, err := d.CheckHost(hostname)
	if err != nil {
		t.Errorf("Error while matching host %s: %s", hostname, err)
	}
	if !ret.IsFiltered {
		t.Errorf("Expected hostname %s to match", hostname)
	}
}

func (d *Dnsfilter) checkMatchEmpty(t *testing.T, hostname string) {
	t.Helper()
	ret, err := d.CheckHost(hostname)
	if err != nil {
		t.Errorf("Error while matching host %s: %s", hostname, err)
	}
	if ret.IsFiltered {
		t.Errorf("Expected hostname %s to not match", hostname)
	}
}

func loadTestRules(d *Dnsfilter) error {
	filterFileName := "../tests/dns.txt"
	file, err := os.Open(filterFileName)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		rule := scanner.Text()
		err = d.AddRule(rule, 0)
		if err == ErrInvalidSyntax {
			continue
		}
		if err != nil {
			return err
		}
	}

	err = scanner.Err()
	return err
}

func NewForTest() *Dnsfilter {
	d := New()
	purgeCaches()
	return d
}

//
// tests
//
func TestSanityCheck(t *testing.T) {
	d := NewForTest()
	defer d.Destroy()
	d.checkAddRule(t, "||doubleclick.net^")
	d.checkMatch(t, "www.doubleclick.net")
	d.checkMatchEmpty(t, "nodoubleclick.net")
	d.checkMatchEmpty(t, "doubleclick.net.ru")
	d.checkMatchEmpty(t, "wmconvirus.narod.ru")
	d.checkAddRuleFail(t, "lkfaojewhoawehfwacoefawr$@#$@3413841384")
}

func TestCount(t *testing.T) {
	d := NewForTest()
	defer d.Destroy()
	err := loadTestRules(d)
	if err != nil {
		t.Fatal(err)
	}
	count := d.Count()
	expected := 12747
	if count != expected {
		t.Fatalf("Number of rules parsed should be %d, but it is %d\n", expected, count)
	}
}

func TestDnsFilterBlocking(t *testing.T) {
	d := NewForTest()
	defer d.Destroy()
	d.checkAddRule(t, "||example.org^")

	d.checkMatch(t, "example.org")
	d.checkMatch(t, "test.example.org")
	d.checkMatch(t, "test.test.example.org")
	d.checkMatchEmpty(t, "testexample.org")
	d.checkMatchEmpty(t, "onemoreexample.org")
}

func TestDnsFilterWhitelist(t *testing.T) {
	d := NewForTest()
	defer d.Destroy()
	d.checkAddRule(t, "||example.org^")
	d.checkAddRule(t, "@@||test.example.org")

	d.checkMatch(t, "example.org")
	d.checkMatchEmpty(t, "test.example.org")
	d.checkMatchEmpty(t, "test.test.example.org")
}

func TestDnsFilterImportant(t *testing.T) {
	d := NewForTest()
	defer d.Destroy()
	d.checkAddRule(t, "@@||example.org^")
	d.checkAddRule(t, "||test.example.org^$important")

	d.checkMatchEmpty(t, "example.org")
	d.checkMatch(t, "test.example.org")
	d.checkMatch(t, "test.test.example.org")
	d.checkMatchEmpty(t, "testexample.org")
	d.checkMatchEmpty(t, "onemoreexample.org")
}

func TestDnsFilterRegexrule(t *testing.T) {
	d := NewForTest()
	defer d.Destroy()
	d.checkAddRule(t, "/example\\.org/")
	d.checkAddRule(t, "@@||test.example.org^")

	d.checkMatch(t, "example.org")
	d.checkMatchEmpty(t, "test.example.org")
	d.checkMatchEmpty(t, "test.test.example.org")
	d.checkMatch(t, "testexample.org")
	d.checkMatch(t, "onemoreexample.org")
}

func TestDomainMask(t *testing.T) {
	d := NewForTest()
	defer d.Destroy()
	d.checkAddRule(t, "test*.example.org^")
	d.checkAddRule(t, "exam*.com")

	d.checkMatch(t, "test.example.org")
	d.checkMatch(t, "test2.example.org")
	d.checkMatch(t, "example.com")
	d.checkMatch(t, "exampleeee.com")

	d.checkMatchEmpty(t, "example.org")
	d.checkMatchEmpty(t, "testexample.org")
	d.checkMatchEmpty(t, "example.co.uk")
}

func TestAddRuleFail(t *testing.T) {
	d := NewForTest()
	defer d.Destroy()
	d.checkAddRuleFail(t, "lkfaojewhoawehfwacoefawr$@#$@3413841384")
}

func TestLotsOfRulesMemoryUsage(t *testing.T) {
	var start, afterLoad, end runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&start)
	fmt.Printf("Memory usage before loading rules - %d kB alloc, %d kB sys\n", start.Alloc/1024, start.Sys/1024)

	d := NewForTest()
	defer d.Destroy()
	err := loadTestRules(d)
	if err != nil {
		t.Error(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&afterLoad)
	fmt.Printf("Memory usage after loading rules - %d kB alloc, %d kB sys\n", afterLoad.Alloc/1024, afterLoad.Sys/1024)

	tests := []struct {
		host  string
		match bool
	}{
		{"asdasdasd_adsajdasda_asdasdjashdkasdasdasdasd_adsajdasda_asdasdjashdkasd.thisistesthost.com", false},
		{"asdasdasd_adsajdasda_asdasdjashdkasdasdasdasd_adsajdasda_asdasdjashdkasd.ad.doubleclick.net", true},
	}
	for _, testcase := range tests {
		ret, err := d.CheckHost(testcase.host)
		if err != nil {
			t.Errorf("Error while matching host %s: %s", testcase.host, err)
		}
		if !ret.IsFiltered && ret.IsFiltered != testcase.match {
			t.Errorf("Expected hostname %s to not match", testcase.host)
		}
		if ret.IsFiltered && ret.IsFiltered != testcase.match {
			t.Errorf("Expected hostname %s to match", testcase.host)
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&end)
	fmt.Printf("Memory usage after matching - %d kB alloc, %d kB sys\n", afterLoad.Alloc/1024, afterLoad.Sys/1024)
}

func TestSafeBrowsing(t *testing.T) {
	testCases := []string{
		"",
		"sb.adtidy.org",
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s in %s", tc, _Func()), func(t *testing.T) {
			d := NewForTest()
			defer d.Destroy()
			d.EnableSafeBrowsing()
			stats.Safebrowsing.Requests = 0
			d.checkMatch(t, "wmconvirus.narod.ru")
			d.checkMatch(t, "wmconvirus.narod.ru")
			if stats.Safebrowsing.Requests != 1 {
				t.Errorf("Safebrowsing lookup positive cache is not working: %v", stats.Safebrowsing.Requests)
			}
			d.checkMatch(t, "WMconvirus.narod.ru")
			if stats.Safebrowsing.Requests != 1 {
				t.Errorf("Safebrowsing lookup positive cache is not working: %v", stats.Safebrowsing.Requests)
			}
			d.checkMatch(t, "wmconvirus.narod.ru.")
			d.checkMatch(t, "test.wmconvirus.narod.ru")
			d.checkMatch(t, "test.wmconvirus.narod.ru.")
			d.checkMatchEmpty(t, "yandex.ru")
			d.checkMatchEmpty(t, "pornhub.com")
			l := stats.Safebrowsing.Requests
			d.checkMatchEmpty(t, "pornhub.com")
			if stats.Safebrowsing.Requests != l {
				t.Errorf("Safebrowsing lookup negative cache is not working: %v", stats.Safebrowsing.Requests)
			}
		})
	}
}

func TestParallelSB(t *testing.T) {
	d := NewForTest()
	defer d.Destroy()
	d.EnableSafeBrowsing()
	t.Run("group", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			t.Run(fmt.Sprintf("aaa%d", i), func(t *testing.T) {
				t.Parallel()
				d.checkMatch(t, "wmconvirus.narod.ru")
				d.checkMatch(t, "wmconvirus.narod.ru.")
				d.checkMatch(t, "test.wmconvirus.narod.ru")
				d.checkMatch(t, "test.wmconvirus.narod.ru.")
				d.checkMatchEmpty(t, "yandex.ru")
				d.checkMatchEmpty(t, "pornhub.com")
			})
		}
	})
}

// the only way to verify that custom server option is working is to point it at a server that does serve safebrowsing
func TestSafeBrowsingCustomServerFail(t *testing.T) {
	d := NewForTest()
	defer d.Destroy()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// w.Write("Hello, client")
		fmt.Fprintln(w, "Hello, client")
	}))
	defer ts.Close()
	address := ts.Listener.Addr().String()

	d.EnableSafeBrowsing()
	d.SetHTTPTimeout(time.Second * 5)
	d.SetSafeBrowsingServer(address) // this will ensure that test fails
	d.checkMatchEmpty(t, "wmconvirus.narod.ru")
}

func TestParentalControl(t *testing.T) {
	d := NewForTest()
	defer d.Destroy()
	d.EnableParental(3)
	d.checkMatch(t, "pornhub.com")
	d.checkMatch(t, "pornhub.com")
	if stats.Parental.Requests != 1 {
		t.Errorf("Parental lookup positive cache is not working")
	}
	d.checkMatch(t, "PORNhub.com")
	if stats.Parental.Requests != 1 {
		t.Errorf("Parental lookup positive cache is not working")
	}
	d.checkMatch(t, "www.pornhub.com")
	d.checkMatch(t, "pornhub.com.")
	d.checkMatch(t, "www.pornhub.com.")
	d.checkMatchEmpty(t, "www.yandex.ru")
	d.checkMatchEmpty(t, "yandex.ru")
	l := stats.Parental.Requests
	d.checkMatchEmpty(t, "yandex.ru")
	if stats.Parental.Requests != l {
		t.Errorf("Parental lookup negative cache is not working")
	}
}

func TestSafeSearch(t *testing.T) {
	d := NewForTest()
	defer d.Destroy()
	_, ok := d.SafeSearchDomain("www.google.com")
	if ok {
		t.Errorf("Expected safesearch to error when disabled")
	}
	d.EnableSafeSearch()
	val, ok := d.SafeSearchDomain("www.google.com")
	if !ok {
		t.Errorf("Expected safesearch to find result for www.google.com")
	}
	if val != "forcesafesearch.google.com" {
		t.Errorf("Expected safesearch for google.com to be forcesafesearch.google.com")
	}
}

//
// parametrized testing
//
var blockingRules = []string{"||example.org^"}
var whitelistRules = []string{"||example.org^", "@@||test.example.org"}
var importantRules = []string{"@@||example.org^", "||test.example.org^$important"}
var regexRules = []string{"/example\\.org/", "@@||test.example.org^"}
var maskRules = []string{"test*.example.org^", "exam*.com"}

var tests = []struct {
	testname   string
	rules      []string
	hostname   string
	isFiltered bool
	reason     Reason
}{
	{"sanity", []string{"||doubleclick.net^"}, "www.doubleclick.net", true, FilteredBlackList},
	{"sanity", []string{"||doubleclick.net^"}, "nodoubleclick.net", false, NotFilteredNotFound},
	{"sanity", []string{"||doubleclick.net^"}, "doubleclick.net.ru", false, NotFilteredNotFound},
	{"sanity", []string{"||doubleclick.net^"}, "wmconvirus.narod.ru", false, NotFilteredNotFound},
	{"blocking", blockingRules, "example.org", true, FilteredBlackList},
	{"blocking", blockingRules, "test.example.org", true, FilteredBlackList},
	{"blocking", blockingRules, "test.test.example.org", true, FilteredBlackList},
	{"blocking", blockingRules, "testexample.org", false, NotFilteredNotFound},
	{"blocking", blockingRules, "onemoreexample.org", false, NotFilteredNotFound},
	{"whitelist", whitelistRules, "example.org", true, FilteredBlackList},
	{"whitelist", whitelistRules, "test.example.org", false, NotFilteredWhiteList},
	{"whitelist", whitelistRules, "test.test.example.org", false, NotFilteredWhiteList},
	{"whitelist", whitelistRules, "testexample.org", false, NotFilteredNotFound},
	{"whitelist", whitelistRules, "onemoreexample.org", false, NotFilteredNotFound},
	{"important", importantRules, "example.org", false, NotFilteredWhiteList},
	{"important", importantRules, "test.example.org", true, FilteredBlackList},
	{"important", importantRules, "test.test.example.org", true, FilteredBlackList},
	{"important", importantRules, "testexample.org", false, NotFilteredNotFound},
	{"important", importantRules, "onemoreexample.org", false, NotFilteredNotFound},
	{"regex", regexRules, "example.org", true, FilteredBlackList},
	{"regex", regexRules, "test.example.org", false, NotFilteredWhiteList},
	{"regex", regexRules, "test.test.example.org", false, NotFilteredWhiteList},
	{"regex", regexRules, "testexample.org", true, FilteredBlackList},
	{"regex", regexRules, "onemoreexample.org", true, FilteredBlackList},
	{"mask", maskRules, "test.example.org", true, FilteredBlackList},
	{"mask", maskRules, "test2.example.org", true, FilteredBlackList},
	{"mask", maskRules, "example.com", true, FilteredBlackList},
	{"mask", maskRules, "exampleeee.com", true, FilteredBlackList},
	{"mask", maskRules, "onemoreexamsite.com", true, FilteredBlackList},
	{"mask", maskRules, "example.org", false, NotFilteredNotFound},
	{"mask", maskRules, "testexample.org", false, NotFilteredNotFound},
	{"mask", maskRules, "example.co.uk", false, NotFilteredNotFound},
}

func TestMatching(t *testing.T) {
	for _, test := range tests {
		t.Run(fmt.Sprintf("%s-%s", test.testname, test.hostname), func(t *testing.T) {
			d := NewForTest()
			defer d.Destroy()
			for _, rule := range test.rules {
				err := d.AddRule(rule, 0)
				if err != nil {
					t.Fatal(err)
				}
			}
			ret, err := d.CheckHost(test.hostname)
			if err != nil {
				t.Errorf("Error while matching host %s: %s", test.hostname, err)
			}
			if ret.IsFiltered != test.isFiltered {
				t.Errorf("Hostname %s has wrong result (%v must be %v)", test.hostname, ret.IsFiltered, test.isFiltered)
			}
			if ret.Reason != test.reason {
				t.Errorf("Hostname %s has wrong reason (%v must be %v)", test.hostname, ret.Reason.String(), test.reason.String())
			}
		})
	}
}

//
// benchmarks
//
func BenchmarkAddRule(b *testing.B) {
	d := NewForTest()
	defer d.Destroy()
	for n := 0; n < b.N; n++ {
		rule := "||doubleclick.net^"
		err := d.AddRule(rule, 0)
		switch err {
		case nil:
		case ErrInvalidSyntax: // ignore invalid syntax
		default:
			b.Fatalf("Error while adding rule %s: %s", rule, err)
		}
	}
}

func BenchmarkAddRuleParallel(b *testing.B) {
	d := NewForTest()
	defer d.Destroy()
	rule := "||doubleclick.net^"
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var err error
		for pb.Next() {
			err = d.AddRule(rule, 0)
		}
		switch err {
		case nil:
		case ErrInvalidSyntax: // ignore invalid syntax
		default:
			b.Fatalf("Error while adding rule %s: %s", rule, err)
		}
	})
}

func BenchmarkLotsOfRulesNoMatch(b *testing.B) {
	d := NewForTest()
	defer d.Destroy()
	err := loadTestRules(d)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		hostname := "asdasdasd_adsajdasda_asdasdjashdkasdasdasdasd_adsajdasda_asdasdjashdkasd.thisistesthost.com"
		ret, err := d.CheckHost(hostname)
		if err != nil {
			b.Errorf("Error while matching host %s: %s", hostname, err)
		}
		if ret.IsFiltered {
			b.Errorf("Expected hostname %s to not match", hostname)
		}
	}
}

func BenchmarkLotsOfRulesNoMatchParallel(b *testing.B) {
	d := NewForTest()
	defer d.Destroy()
	err := loadTestRules(d)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	hostname := "asdasdasd_adsajdasda_asdasdjashdkasdasdasdasd_adsajdasda_asdasdjashdkasd.thisistesthost.com"
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ret, err := d.CheckHost(hostname)
			if err != nil {
				b.Errorf("Error while matching host %s: %s", hostname, err)
			}
			if ret.IsFiltered {
				b.Errorf("Expected hostname %s to not match", hostname)
			}
		}
	})
}

func BenchmarkLotsOfRulesMatch(b *testing.B) {
	d := NewForTest()
	defer d.Destroy()
	err := loadTestRules(d)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		const hostname = "asdasdasd_adsajdasda_asdasdjashdkasdasdasdasd_adsajdasda_asdasdjashdkasd.ad.doubleclick.net"
		ret, err := d.CheckHost(hostname)
		if err != nil {
			b.Errorf("Error while matching host %s: %s", hostname, err)
		}
		if !ret.IsFiltered {
			b.Errorf("Expected hostname %s to match", hostname)
		}
	}
}

func BenchmarkLotsOfRulesMatchParallel(b *testing.B) {
	d := NewForTest()
	defer d.Destroy()
	err := loadTestRules(d)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	const hostname = "asdasdasd_adsajdasda_asdasdjashdkasdasdasdasd_adsajdasda_asdasdjashdkasd.ad.doubleclick.net"
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ret, err := d.CheckHost(hostname)
			if err != nil {
				b.Errorf("Error while matching host %s: %s", hostname, err)
			}
			if !ret.IsFiltered {
				b.Errorf("Expected hostname %s to match", hostname)
			}
		}
	})
}

func BenchmarkSafeBrowsing(b *testing.B) {
	d := NewForTest()
	defer d.Destroy()
	d.EnableSafeBrowsing()
	for n := 0; n < b.N; n++ {
		hostname := "wmconvirus.narod.ru"
		ret, err := d.CheckHost(hostname)
		if err != nil {
			b.Errorf("Error while matching host %s: %s", hostname, err)
		}
		if !ret.IsFiltered {
			b.Errorf("Expected hostname %s to match", hostname)
		}
	}
}

func BenchmarkSafeBrowsingParallel(b *testing.B) {
	d := NewForTest()
	defer d.Destroy()
	d.EnableSafeBrowsing()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			hostname := "wmconvirus.narod.ru"
			ret, err := d.CheckHost(hostname)
			if err != nil {
				b.Errorf("Error while matching host %s: %s", hostname, err)
			}
			if !ret.IsFiltered {
				b.Errorf("Expected hostname %s to match", hostname)
			}
		}
	})
}

func BenchmarkSafeSearch(b *testing.B) {
	d := NewForTest()
	defer d.Destroy()
	d.EnableSafeSearch()
	for n := 0; n < b.N; n++ {
		val, ok := d.SafeSearchDomain("www.google.com")
		if !ok {
			b.Errorf("Expected safesearch to find result for www.google.com")
		}
		if val != "forcesafesearch.google.com" {
			b.Errorf("Expected safesearch for google.com to be forcesafesearch.google.com")
		}
	}
}

func BenchmarkSafeSearchParallel(b *testing.B) {
	d := NewForTest()
	defer d.Destroy()
	d.EnableSafeSearch()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			val, ok := d.SafeSearchDomain("www.google.com")
			if !ok {
				b.Errorf("Expected safesearch to find result for www.google.com")
			}
			if val != "forcesafesearch.google.com" {
				b.Errorf("Expected safesearch for google.com to be forcesafesearch.google.com")
			}
		}
	})
}

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

//
// helper functions for debugging and testing
//
func purgeCaches() {
	safebrowsingCache.Purge()
	parentalCache.Purge()
}

func _Func() string {
	pc := make([]uintptr, 10) // at least 1 entry needed
	runtime.Callers(2, pc)
	f := runtime.FuncForPC(pc[0])
	return path.Base(f.Name())
}

func trace(format string, args ...interface{}) {
	pc := make([]uintptr, 10) // at least 1 entry needed
	runtime.Callers(2, pc)
	f := runtime.FuncForPC(pc[0])
	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("%s(): ", path.Base(f.Name())))
	text := fmt.Sprintf(format, args...)
	buf.WriteString(text)
	if len(text) == 0 || text[len(text)-1] != '\n' {
		buf.WriteRune('\n')
	}
	fmt.Print(buf.String())
}
