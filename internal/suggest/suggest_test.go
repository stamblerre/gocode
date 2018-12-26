package suggest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stamblerre/gocode/internal/suggest"
)

var testDirFlag = flag.String("testdir", "", "specify a directory to run the test on")

func TestRegress(t *testing.T) {
	testDirs, err := filepath.Glob("testdata/test.*")
	if err != nil {
		t.Fatal(err)
	}
	tmpDir, err := filepath.Abs("tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tmpDir, 0775); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpDir)
	if *testDirFlag != "" {
		t.Run(*testDirFlag, func(t *testing.T) {
			testRegress(t, "testdata/test."+*testDirFlag)
		})
	} else {
		t.Run("all", func(t *testing.T) {
			for _, testDir := range testDirs {
				// Skip test.0011 for Go <= 1.11 because a method was added to reflect.Value.
				// TODO(rstambler): Change this when Go 1.12 comes out.
				if !strings.HasPrefix(runtime.Version(), "devel") && strings.HasSuffix(testDir, "test.0011") {
					continue
				}
				testDir := testDir // capture
				name := strings.TrimPrefix(testDir, "testdata/")
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					testRegress(t, testDir)
				})
			}
		})
	}
}

func testRegress(t *testing.T, testDir string) {
	testDir, err := filepath.Abs(testDir)
	if err != nil {
		t.Errorf("Abs failed: %v", err)
		return
	}
	tmpTestDir := strings.Replace(testDir, "testdata", "tmp", 1)
	if err := os.MkdirAll(tmpTestDir, 0775); err != nil {
		t.Errorf("MkdirAll failed: %v", err)
		return
	}
	defer os.RemoveAll(tmpTestDir)

	files, err := ioutil.ReadDir(testDir)
	if err != nil {
		t.Error(err)
	}
	var tmpTestFile string
	var data []byte
	var cursor int
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".go") {
			// Copy any Go files to a temporary directory.
			filename := filepath.Join(testDir, file.Name())
			d, err := ioutil.ReadFile(filename)
			if err != nil {
				t.Errorf("ReadFile failed: %v", err)
				return
			}
			tmpTestFile = filepath.Join(tmpTestDir, file.Name())
			if err := ioutil.WriteFile(tmpTestFile, d, 0775); err != nil {
				t.Errorf("WriteFile failed: %v", err)
				return
			}
		} else if strings.HasSuffix(file.Name(), ".go.in") {
			// Copy the test files to the temporary directory and save information.
			filename := filepath.Join(testDir, file.Name())
			var err error
			data, err = ioutil.ReadFile(filename)
			if err != nil {
				t.Errorf("ReadFile failed: %v", err)
				return
			}
			cursor = bytes.IndexByte(data, '@')
			if cursor < 0 {
				t.Errorf("Missing @")
				return
			}
			data = append(data[:cursor], data[cursor+1:]...)
			tmpTestFile = filepath.Join(tmpTestDir, strings.TrimSuffix(file.Name(), ".in"))
			if err := ioutil.WriteFile(tmpTestFile, data, 0775); err != nil {
				t.Errorf("WriteFile failed: %v", err)
				return
			}
		}
	}
	cfg := suggest.Config{
		Logf:    func(string, ...interface{}) {},
		Context: &suggest.PackedContext{},
	}
	if testing.Verbose() {
		cfg.Logf = t.Logf
	}

	if cfgJSON, err := os.Open(filepath.Join(testDir, "config.json")); err == nil {
		if err := json.NewDecoder(cfgJSON).Decode(&cfg); err != nil {
			t.Errorf("Decode failed: %v", err)
			return
		}
	} else if !os.IsNotExist(err) {
		t.Errorf("Open failed: %v", err)
		return
	}
	candidates, prefixLen := cfg.Suggest(tmpTestFile, data, cursor)

	var out bytes.Buffer
	suggest.NiceFormat(&out, candidates, prefixLen)
	want, _ := ioutil.ReadFile(filepath.Join(testDir, "out.expected"))
	if got := out.Bytes(); !bytes.Equal(got, want) {
		t.Errorf("%s:\nGot:\n%s\nWant:\n%s\n", testDir, got, want)
		return
	}
	return
}

func TestCancellation(t *testing.T) {
	// sanity check for cancellation
	ctx, cancel := context.WithCancel(context.Background())

	cfg := suggest.Config{
		RequestContext: ctx,
		Logf:           func(string, ...interface{}) {},
		Context:        &suggest.PackedContext{},
	}
	if testing.Verbose() {
		cfg.Logf = t.Logf
	}

	data, err := ioutil.ReadFile("suggest_test.go")
	if err != nil {
		t.Fatal(err)
	}

	cancel()
	_, _ = cfg.Suggest("suggest_test.go", data, 100)
}
