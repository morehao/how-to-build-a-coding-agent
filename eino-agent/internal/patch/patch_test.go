package patch

import (
	"strings"
	"testing"
)

func TestParseAndApplySingleHunk(t *testing.T) {
	input := `--- a/hello.txt
+++ b/hello.txt
@@ -1,3 +1,3 @@
 one
-two
+TWO
 three
`
	files, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path() != "hello.txt" {
		t.Fatalf("files = %+v", files)
	}
	out, err := Apply("one\ntwo\nthree\n", files[0])
	if err != nil {
		t.Fatal(err)
	}
	if out != "one\nTWO\nthree\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestParseMultipleFilesAndNewFile(t *testing.T) {
	input := `--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+hello
+world
--- a/a.txt
+++ b/a.txt
@@ -1 +1 @@
-old
+new
`
	files, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %+v", files)
	}
	out, err := Apply("", files[0])
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello\nworld\n" {
		t.Fatalf("new = %q", out)
	}
	out, err = Apply("old\n", files[1])
	if err != nil || out != "new\n" {
		t.Fatalf("edit = %q, %v", out, err)
	}
}

func TestApplyRejectsContextMismatch(t *testing.T) {
	input := `--- a/x
+++ b/x
@@ -1 +1 @@
-old
+new
`
	files, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply("different\n", files[0]); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("err = %v", err)
	}
}

func TestMalformedPatchRejected(t *testing.T) {
	if _, err := Parse("not a patch"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Parse("--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n-old\n+new\n"); err == nil {
		t.Fatal("expected line count error")
	}
}
