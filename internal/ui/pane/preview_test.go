package pane_test

import (
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stxkxs/agentfs/internal/fileview"
	"github.com/stxkxs/agentfs/internal/fsx"
	"github.com/stxkxs/agentfs/internal/ui/keys"
	"github.com/stxkxs/agentfs/internal/ui/layout"
	"github.com/stxkxs/agentfs/internal/ui/pane"
	"github.com/stxkxs/agentfs/internal/ui/theme"
)

// jsonDoc carries one token of every class a JSON lexer names, so a claim about
// how a class is drawn is made against a document that holds all of them.
const jsonDoc = "{\n  \"task\": \"review the search index\",\n  \"steps\": 3,\n" +
	"  \"blocked\": true,\n  \"problem\": null\n}\n"

// logDoc carries one line of every level a log lexer names.
const logDoc = "TRACE opening\nDEBUG resolving\nINFO started\nWARN retrying\nERROR gave up\n"

func loadWindow(t *testing.T, name, body string, opts fileview.Options) *fileview.Window {
	t.Helper()
	w := fileview.Load(fsx.New("ws", fstest.MapFS{name: {Data: []byte(body)}}), name, opts)
	if err := w.Err(); err != nil {
		t.Fatalf("loading %s: %v", name, err)
	}
	return w
}

// numbered returns n lines whose text identifies the line it came from, so a
// claim about which part of a file is on screen names a line rather than a row.
func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("line-")
		if i < 10 {
			b.WriteByte('0')
		}
		b.WriteString(strconv.Itoa(i))
		b.WriteByte('\n')
	}
	return b.String()
}

// staleStatFS reports a file's size as it was before an append that landed
// between the stat and the read of the range that stat sized.
type staleStatFS struct {
	fsx.FS
	name string
	size int64
}

func (s staleStatFS) Stat(name string) (fs.FileInfo, error) {
	info, err := s.FS.Stat(name)
	if err != nil || name != s.name {
		return info, err
	}
	return staleInfo{FileInfo: info, size: s.size}, nil
}

type staleInfo struct {
	fs.FileInfo
	size int64
}

func (i staleInfo) Size() int64 { return i.size }

func TestPreviewScrollsThroughAFileAndStopsAtBothEnds(t *testing.T) {
	t.Parallel()
	w := loadWindow(t, "notes.txt", numbered(40), fileview.Options{})
	r := layout.Rect{W: 40, H: 10}

	var v pane.Preview
	v.SetPath("notes.txt")

	top := func() string { return v.View(w, r, theme.Plain())[0] }
	if !strings.Contains(top(), "line-01") {
		t.Fatalf("a fresh preview does not start at the top of the file: %q", top())
	}

	for _, tc := range []struct {
		name   string
		action keys.Action
		moved  bool
		want   string
	}{
		{name: "down", action: keys.ActionDown, moved: true, want: "line-02"},
		{name: "page down", action: keys.ActionPageDown, moved: true, want: "line-07"},
		{name: "page up", action: keys.ActionPageUp, moved: true, want: "line-02"},
		{name: "up", action: keys.ActionUp, moved: true, want: "line-01"},
		{name: "up at the top", action: keys.ActionUp, moved: false, want: "line-01"},
		{name: "bottom", action: keys.ActionBottom, moved: true, want: "line-31"},
		{name: "down at the bottom", action: keys.ActionDown, moved: false, want: "line-31"},
		{name: "top", action: keys.ActionTop, moved: true, want: "line-01"},
		{name: "an action the pane does not scroll for", action: keys.ActionToggleHelp, want: "line-01"},
	} {
		if got := v.Update(tc.action, w, r.H); got != tc.moved {
			t.Errorf("%s: Update reported moved=%t, want %t", tc.name, got, tc.moved)
		}
		if !strings.Contains(top(), tc.want) {
			t.Errorf("%s: the top of the viewport is %q, want it to hold %s", tc.name, top(), tc.want)
		}
	}
}

// The last line of a file is reachable, so a reader following a log is never
// held one row short of the entry they are waiting for.
func TestPreviewBottomBringsTheLastLineOnScreen(t *testing.T) {
	t.Parallel()
	w := loadWindow(t, "notes.txt", numbered(40), fileview.Options{})
	r := layout.Rect{W: 40, H: 10}

	var v pane.Preview
	v.SetPath("notes.txt")
	v.Update(keys.ActionBottom, w, r.H)

	if out := strings.Join(v.View(w, r, theme.Plain()), "\n"); !strings.Contains(out, "line-40") {
		t.Fatalf("the end of the file is not reachable:\n%s", out)
	}
}

func TestPreviewIgnoresNavigationWithNoFileLoaded(t *testing.T) {
	t.Parallel()
	var v pane.Preview
	v.SetPath("notes.txt")
	for _, a := range []keys.Action{keys.ActionDown, keys.ActionBottom, keys.ActionNextMatch} {
		if v.Update(a, nil, 10) {
			t.Errorf("%v moved a preview with no window", a)
		}
	}
}

// Selecting another file starts that file at its beginning and drops the query
// the reader ran against the previous one, so a search does not silently follow
// them from file to file.
func TestPreviewSetPathResetsPositionAndClearsTheQuery(t *testing.T) {
	t.Parallel()
	w := loadWindow(t, "notes.txt", numbered(40), fileview.Options{})
	r := layout.Rect{W: 40, H: 10}

	var v pane.Preview
	v.SetPath("notes.txt")
	v.Update(keys.ActionBottom, w, r.H)
	v.Search(w, "line-33")

	v.SetPath("notes.txt")
	if !strings.Contains(v.View(w, r, theme.Plain())[0], "line-31") {
		t.Error("selecting the file already on display moved the reader's position")
	}
	if v.Query() != "line-33" {
		t.Errorf("Query() = %q, want the query to survive selecting the same file", v.Query())
	}

	v.SetPath("other.txt")
	if v.Path() != "other.txt" {
		t.Errorf("Path() = %q, want other.txt", v.Path())
	}
	if v.Query() != "" {
		t.Errorf("Query() = %q, want the previous file's query dropped", v.Query())
	}
	if at, total := v.Matches(); at != 0 || total != 0 {
		t.Errorf("Matches() = (%d, %d), want no matches carried across files", at, total)
	}
	if !strings.Contains(v.View(w, r, theme.Plain())[0], "line-01") {
		t.Error("selecting another file did not open it at its beginning")
	}
}

func TestPreviewSearchCountsEveryMatchingLine(t *testing.T) {
	t.Parallel()
	w := loadWindow(t, "agent.log", "alpha\nbeta\nalpha again\ngamma\nALPHA\n", fileview.Options{})

	var v pane.Preview
	v.SetPath("agent.log")
	if at, total := v.Matches(); at != 0 || total != 0 {
		t.Errorf("Matches() = (%d, %d) before a search, want none", at, total)
	}

	v.Search(w, "alpha")
	if at, total := v.Matches(); at != 1 || total != 3 {
		t.Errorf("Matches() = (%d, %d), want the first of three", at, total)
	}

	v.Search(w, "nothing here")
	if at, total := v.Matches(); at != 0 || total != 0 {
		t.Errorf("Matches() = (%d, %d) for a query nothing matches, want none", at, total)
	}

	v.Search(nil, "alpha")
	if at, total := v.Matches(); at != 0 || total != 0 {
		t.Errorf("Matches() = (%d, %d) with no window, want none", at, total)
	}
}

// Stepping through matches wraps in both directions, so the last match is one
// press from the first rather than a dead end.
func TestPreviewSteppingThroughMatchesWraps(t *testing.T) {
	t.Parallel()
	w := loadWindow(t, "notes.txt", numbered(40), fileview.Options{})

	var v pane.Preview
	v.SetPath("notes.txt")
	if v.Update(keys.ActionNextMatch, w, 10) {
		t.Error("stepping to the next match moved a preview with no search running")
	}

	v.Search(w, "line-3")
	_, total := v.Matches()
	if total != 10 {
		t.Fatalf("Matches() found %d lines, want the ten holding line-3", total)
	}

	for want := 2; want <= total; want++ {
		if !v.Update(keys.ActionNextMatch, w, 10) {
			t.Fatalf("stepping to match %d reported no movement", want)
		}
		if at, _ := v.Matches(); at != want {
			t.Fatalf("Matches() = %d, want %d", at, want)
		}
	}
	v.Update(keys.ActionNextMatch, w, 10)
	if at, _ := v.Matches(); at != 1 {
		t.Errorf("Matches() = %d after the last match, want it to wrap to 1", at)
	}
	v.Update(keys.ActionPrevMatch, w, 10)
	if at, _ := v.Matches(); at != total {
		t.Errorf("Matches() = %d stepping back from the first match, want %d", at, total)
	}
}

// A match arrives with the lines around it, because a hit rendered against the
// top edge of the viewport hides the context that makes it readable.
func TestPreviewCentresTheMatchItStepsTo(t *testing.T) {
	t.Parallel()
	w := loadWindow(t, "notes.txt", numbered(40), fileview.Options{})
	r := layout.Rect{W: 40, H: 11}

	var v pane.Preview
	v.SetPath("notes.txt")
	v.Search(w, "line-20")
	v.Update(keys.ActionNextMatch, w, r.H)

	out := v.View(w, r, theme.Plain())
	row := -1
	for i, line := range out {
		if strings.Contains(line, "line-20") {
			row = i
		}
	}
	if row != r.H/2 {
		t.Fatalf("the match sits on row %d of %d, want the middle:\n%s", row, r.H, strings.Join(out, "\n"))
	}
}

func TestPreviewClearSearchDropsTheQueryOnce(t *testing.T) {
	t.Parallel()
	w := loadWindow(t, "notes.txt", numbered(40), fileview.Options{})

	var v pane.Preview
	v.SetPath("notes.txt")
	if v.Update(keys.ActionClearSearch, w, 10) {
		t.Error("clearing a search that is not running reported a change")
	}

	v.Search(w, "line-07")
	if !v.Update(keys.ActionClearSearch, w, 10) {
		t.Fatal("clearing a running search reported no change")
	}
	if v.Query() != "" {
		t.Errorf("Query() = %q after clearing, want it empty", v.Query())
	}
	if v.Update(keys.ActionClearSearch, w, 10) {
		t.Error("clearing an already cleared search reported a change")
	}
}

// The badge answers what the pane is showing and what it is not: how much of
// the file is on screen, which end of it was elided, and where the reader is in
// the search.
func TestPreviewBadgeNamesLinesTruncationAndMatches(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("abcdefghij\n", 300)

	t.Run("nothing selected", func(t *testing.T) {
		t.Parallel()
		var v pane.Preview
		if got := (pane.Preview{}).Badge(loadWindow(t, "notes.txt", "one\n", fileview.Options{})); got != "" {
			t.Errorf("Badge() = %q with no file selected, want it empty", got)
		}
		v.SetPath("notes.txt")
		if got := v.Badge(nil); got != "" {
			t.Errorf("Badge() = %q with no window, want it empty", got)
		}
	})

	t.Run("whole file", func(t *testing.T) {
		t.Parallel()
		var v pane.Preview
		v.SetPath("notes.txt")
		if got := v.Badge(loadWindow(t, "notes.txt", "one\ntwo\nthree\n", fileview.Options{})); got != "3 lines" {
			t.Errorf("Badge() = %q, want %q", got, "3 lines")
		}
	})

	t.Run("head of a file", func(t *testing.T) {
		t.Parallel()
		var v pane.Preview
		v.SetPath("agent.log")
		w := loadWindow(t, "agent.log", long, fileview.Options{MaxBytes: 1100})
		if got := v.Badge(w); !strings.Contains(got, "head of 3KiB") {
			t.Errorf("Badge() = %q, want it to name the head of a 3KiB file", got)
		}
	})

	t.Run("tail of a file", func(t *testing.T) {
		t.Parallel()
		var v pane.Preview
		v.SetPath("agent.log")
		w := loadWindow(t, "agent.log", long, fileview.Options{MaxBytes: 1100, Tail: true})
		if got := v.Badge(w); !strings.Contains(got, "tail of 3KiB") {
			t.Errorf("Badge() = %q, want it to name the tail of a 3KiB file", got)
		}
	})

	t.Run("middle of a file", func(t *testing.T) {
		t.Parallel()
		root := fsx.New("ws", staleStatFS{
			FS:   fstest.MapFS{"agent.log": {Data: []byte(long)}},
			name: "agent.log", size: 2200,
		})
		w := fileview.Load(root, "agent.log", fileview.Options{MaxBytes: 1100, Tail: true})
		if err := w.Err(); err != nil {
			t.Fatalf("Load: %v", err)
		}
		var v pane.Preview
		v.SetPath("agent.log")
		if got := v.Badge(w); !strings.Contains(got, "middle of 3KiB") {
			t.Errorf("Badge() = %q, want it to name a window holding neither end", got)
		}
	})

	t.Run("bytes below a kibibyte", func(t *testing.T) {
		t.Parallel()
		var v pane.Preview
		v.SetPath("agent.log")
		w := loadWindow(t, "agent.log", strings.Repeat("ab\n", 100), fileview.Options{MaxBytes: 90})
		if got := v.Badge(w); !strings.Contains(got, "head of 300 B") {
			t.Errorf("Badge() = %q, want a size in bytes", got)
		}
	})

	t.Run("bytes above a mebibyte", func(t *testing.T) {
		t.Parallel()
		var v pane.Preview
		v.SetPath("agent.log")
		w := loadWindow(t, "agent.log", strings.Repeat("ab\n", 700_000), fileview.Options{MaxBytes: 1100})
		if got := v.Badge(w); !strings.Contains(got, "head of 2MiB") {
			t.Errorf("Badge() = %q, want a size in mebibytes", got)
		}
	})

	t.Run("search position", func(t *testing.T) {
		t.Parallel()
		w := loadWindow(t, "notes.txt", numbered(40), fileview.Options{})
		var v pane.Preview
		v.SetPath("notes.txt")

		v.Search(w, "line-1")
		v.Update(keys.ActionNextMatch, w, 10)
		if got := v.Badge(w); !strings.Contains(got, `2/10 for "line-1"`) {
			t.Errorf("Badge() = %q, want the reader's place in the search", got)
		}

		v.Search(w, "line-99")
		if got := v.Badge(w); !strings.Contains(got, `no match for "line-99"`) {
			t.Errorf("Badge() = %q, want a query that matched nothing named as one", got)
		}
	})
}

// A match is drawn over the syntax rather than instead of it: a hit inside a
// JSON string is visible, and the token it sits in keeps its colour on either
// side so the reader can still see where in the structure the hit is.
func TestPreviewDrawsAMatchOverTheSyntaxItSitsIn(t *testing.T) {
	t.Parallel()
	w := loadWindow(t, "state.json", jsonDoc, fileview.Options{})
	p := theme.Dark()

	var v pane.Preview
	v.SetPath("state.json")
	v.Search(w, "search")
	out := strings.Join(v.View(w, layout.Rect{W: 120, H: 8}, p), "\n")

	for _, want := range []struct{ what, text string }{
		{"the match", p.Match(true).Render("search")},
		{"the string before the match", p.JSON(theme.RoleString).Render(`"review the `)},
		{"the string after the match", p.JSON(theme.RoleString).Render(` index"`)},
		{"the key naming the field", p.JSON(theme.RoleKey).Render(`"task"`)},
	} {
		if !strings.Contains(out, want.text) {
			t.Errorf("%s is not drawn: %q missing", want.what, want.text)
		}
	}
}

// A match on a line the reader is not standing on is drawn differently from the
// one they are, or a search has not said where the next press lands.
func TestPreviewDistinguishesTheCurrentMatchFromTheRest(t *testing.T) {
	t.Parallel()
	w := loadWindow(t, "notes.txt", "hit one\nhit two\n", fileview.Options{})
	p := theme.Dark()

	var v pane.Preview
	v.SetPath("notes.txt")
	v.Search(w, "hit")
	out := strings.Join(v.View(w, layout.Rect{W: 60, H: 4}, p), "\n")

	if !strings.Contains(out, p.Match(true).Render("hit")) {
		t.Error("the current match is not drawn as the current one")
	}
	if !strings.Contains(out, p.Match(false).Render("hit")) {
		t.Error("a match the reader is not standing on is not drawn")
	}
}

// Every token class the lexer names is drawn in its own style, so a reader can
// tell a key from a value without reading the punctuation between them.
func TestPreviewDrawsEveryTokenClassInItsOwnStyle(t *testing.T) {
	t.Parallel()
	p := theme.Dark()

	t.Run("json", func(t *testing.T) {
		t.Parallel()
		w := loadWindow(t, "state.json", jsonDoc, fileview.Options{})
		var v pane.Preview
		v.SetPath("state.json")
		out := strings.Join(v.View(w, layout.Rect{W: 120, H: 8}, p), "\n")

		for _, want := range []struct {
			role  theme.JSONRole
			token string
		}{
			{theme.RoleKey, `"steps"`},
			{theme.RoleString, `"review the search index"`},
			{theme.RoleNumber, "3"},
			{theme.RoleBool, "true"},
			{theme.RoleNull, "null"},
			{theme.RolePunct, "{"},
		} {
			if styled := p.JSON(want.role).Render(want.token); !strings.Contains(out, styled) {
				t.Errorf("%v token %q is not drawn in the %v style", want.role, want.token, want.role)
			}
		}
	})

	t.Run("log levels", func(t *testing.T) {
		t.Parallel()
		w := loadWindow(t, "agent.log", logDoc, fileview.Options{})
		var v pane.Preview
		v.SetPath("agent.log")
		out := strings.Join(v.View(w, layout.Rect{W: 120, H: 8}, p), "\n")

		for _, want := range []struct {
			role  theme.LogRole
			token string
		}{
			{theme.RoleTrace, "TRACE"},
			{theme.RoleDebug, "DEBUG"},
			{theme.RoleInfoLevel, "INFO"},
			{theme.RoleWarn, "WARN"},
			{theme.RoleErrorLevel, "ERROR"},
		} {
			if styled := p.LogLevel(want.role).Render(want.token); !strings.Contains(out, styled) {
				t.Errorf("level %q is not drawn in the %v style", want.token, want.role)
			}
		}
	})

	t.Run("text under no role", func(t *testing.T) {
		t.Parallel()
		w := loadWindow(t, "notes.txt", "the quick brown fox\njumped over it\n", fileview.Options{})
		var v pane.Preview
		v.SetPath("notes.txt")
		out := strings.Join(v.View(w, layout.Rect{W: 60, H: 4}, p), "\n")

		if !strings.Contains(out, p.Body().Render("the quick brown fox")) {
			t.Errorf("prose is not drawn as body text:\n%q", out)
		}
	})
}

// A pane with nothing to render says which of the four reasons it is, because
// "loading" and "empty" and "unreadable" call for different responses.
func TestPreviewSaysWhyItHasNothingToShow(t *testing.T) {
	t.Parallel()
	r := layout.Rect{W: 40, H: 4}

	missing := fileview.Load(fsx.New("ws", fstest.MapFS{}), "gone.txt", fileview.Options{})
	if missing.Err() == nil {
		t.Fatal("loading a file that is not there succeeded")
	}

	for _, tc := range []struct {
		name string
		path string
		win  *fileview.Window
		want string
	}{
		{name: "no file selected", want: "select a file"},
		{name: "not read yet", path: "notes.txt", want: "loading"},
		{name: "unreadable", path: "gone.txt", win: missing, want: "gone.txt"},
		{
			name: "empty file", path: "empty.txt", want: "empty file",
			win: loadWindow(t, "empty.txt", "", fileview.Options{}),
		},
	} {
		var v pane.Preview
		v.SetPath(tc.path)
		out := v.View(tc.win, r, theme.Plain())
		assertFits(t, "preview "+tc.name, out, r)
		if !strings.Contains(strings.Join(out, "\n"), tc.want) {
			t.Errorf("%s: the pane does not say so:\n%s", tc.name, strings.Join(out, "\n"))
		}
	}
}

func TestPreviewFitsEveryRect(t *testing.T) {
	t.Parallel()
	w := loadWindow(t, "state.json", strings.Join(hostile, "\n")+"\n"+jsonDoc, fileview.Options{})

	var v pane.Preview
	v.SetPath("state.json")
	v.Search(w, "x")
	for _, r := range rects {
		assertFits(t, "preview", v.View(w, r, theme.Plain()), r)
		assertFits(t, "preview coloured", v.View(w, r, theme.Dark()), r)
	}
}

// Line numbers are right-aligned in a fixed column, so the file's content
// starts at one place on every row however wide the numbers beside it grow.
func TestPreviewAlignsLineNumbersOfDifferingWidths(t *testing.T) {
	t.Parallel()
	w := loadWindow(t, "notes.txt", numbered(10_001), fileview.Options{})
	r := layout.Rect{W: 40, H: 6}

	var v pane.Preview
	v.SetPath("notes.txt")
	v.Update(keys.ActionBottom, w, r.H)

	out := v.View(w, r, theme.Plain())
	assertFits(t, "preview", out, r)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "line-10001") {
		t.Fatalf("the end of a ten-thousand line file is not reachable:\n%s", joined)
	}
	if !strings.Contains(joined, "line-9996") {
		t.Fatalf("the rows above it are not on screen:\n%s", joined)
	}
	col := strings.Index(out[0], "line-")
	if col == 0 {
		t.Fatalf("the rows carry no line-number column:\n%s", joined)
	}
	for i, line := range out {
		if got := strings.Index(line, "line-"); got != col {
			t.Errorf("row %d starts its text at column %d, want %d:\n%s", i, got, col, joined)
		}
		if n := strings.TrimSpace(line[:col]); !strings.Contains(line[col:], "line-"+n) {
			t.Errorf("row %d is numbered %q and shows %q", i, n, strings.TrimRight(line[col:], " "))
		}
	}
}

// A document read while it is being written holds a line the lexer cannot
// tokenize. That line is displayed as text rather than dropped, because a
// half-written state document is what a reader is watching for.
func TestPreviewRendersALineTheLexerCannotTokenize(t *testing.T) {
	t.Parallel()
	w := loadWindow(t, "state.json", "{\n  \"task\": \"half writ\n", fileview.Options{})
	p := theme.Dark()

	var v pane.Preview
	v.SetPath("state.json")
	out := strings.Join(v.View(w, layout.Rect{W: 80, H: 4}, p), "\n")

	if want := p.Body().Render(`  "task": "half writ`); !strings.Contains(out, want) {
		t.Errorf("an untokenizable line is not drawn as plain text:\n%q", out)
	}
}
