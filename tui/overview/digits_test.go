package overview

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

func TestDigitsFocusFirstTenOverallFromAnyPage(t *testing.T) {
	for _, compact := range []bool{false, true} {
		for index, digit := range "1234567890" {
			sessions := viewSessions(20)
			for i := range sessions {
				sessions[i].TabID = 500 - i*7 // digits follow card order, not Kitty IDs
			}
			m, r := atSize(t, sessions, 19, 160, 45)
			if compact {
				m = press(t, m, "c")
			}
			target := m.sessions[index]
			m, cmd := send(t, m, key(t, string(digit)))
			if len(r.focus) != 1 || r.focus[0] != target.TabID || m.focusTabID != target.TabID {
				t.Fatalf("compact=%v digit=%c: focused %v, want tab %d", compact, digit, r.focus, target.TabID)
			}
			if m.selected != index || m.selectedTabID != target.TabID || m.selectedWindowID != target.WindowID {
				t.Fatalf("digit=%c did not select target identity", digit)
			}
			if cmd == nil || quits(t, cmd) {
				t.Fatal("digit focus must wait for confirmation before exiting")
			}
			_, cmd = send(t, m, commands.FocusDoneMsg{})
			if !quits(t, cmd) {
				t.Fatal("confirmed digit focus did not exit")
			}
		}
	}
}

func TestMissingDigitTargetsAreInert(t *testing.T) {
	for count := 0; count < 10; count++ {
		m, r := newTestModel(0)
		m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(count)))
		for _, digit := range "1234567890"[count:] {
			before := m.selected
			next, effect := send(t, m, key(t, string(digit)))
			if effect != nil || next.selected != before || next.focusTabID != 0 || len(r.focus) != 0 {
				t.Fatalf("count=%d digit=%c changed selection or dispatched focus", count, digit)
			}
			m = next
		}
	}
}

func TestDigitFocusIsSingleFlightAndFailureKeepsTarget(t *testing.T) {
	m, r := atSize(t, viewSessions(20), 19, 160, 45)
	m = press(t, m, "0")
	target := m.sessions[9]
	for _, digit := range []string{"1", "0", "9", "enter"} {
		next, cmd := send(t, m, key(t, digit))
		if cmd != nil || next.selected != 9 || next.focusTabID != target.TabID || len(r.focus) != 1 {
			t.Fatal("a repeated focus key retargeted or duplicated pending focus")
		}
		m = next
	}
	m, cmd := send(t, m, commands.FocusDoneMsg{Err: errors.New("focus failed")})
	if quits(t, cmd) || m.focusErr == nil || m.selectedTabID != target.TabID {
		t.Fatal("failed digit focus did not retain target for retry")
	}
	m = press(t, m, "enter")
	if len(r.focus) != 2 || r.focus[1] != target.TabID {
		t.Fatal("Enter did not retry the failed digit target")
	}
}

func TestCardDigitsOccupySecondRowRightEdgeInOverallReadingOrder(t *testing.T) {
	for _, compact := range []bool{false, true} {
		in := viewInput(160, 45, viewSessions(20))
		in.Compact = compact
		g := MeasureMode(in.Width, in.Height, compact)
		for page := 0; page < g.PageCount(len(in.Sessions)); page++ {
			in.Selected = g.PageStart(page)
			lines := renderLines(t, in)
			for pos := 0; pos < g.Capacity(); pos++ {
				index := g.PageStart(page) + pos
				if index >= len(in.Sessions) {
					break
				}
				tile, _ := g.Tile(pos)
				inner := tile.Width - tileBorderCells - 2*tilePadding
				line := ansi.Cut(lines[tile.Y+2], tile.X+1+tilePadding, tile.X+1+tilePadding+inner)
				expected := strings.Split(dashboard.RenderCompactPreview(dashboard.CompactInput{
					Width: inner, Height: tile.Height - tileBorderRows, SummaryOnly: compact,
					Content: dashboard.InspectorLoading,
					Session: in.Sessions[index], Cfg: in.Cfg, Now: in.Now,
				}), "\n")[1]
				if index < 10 {
					digit := fmt.Sprintf("%d", (index+1)%10)
					expected = fitCell(layout.StyledTruncate(expected, inner-2, "…"), inner-1) + styleFooterKey.Render(digit)
				}
				if ansi.Strip(line) != ansi.Strip(fitCell(expected, inner)) {
					t.Fatalf("compact=%v card=%d second row=%q, want %q", compact, index+1, ansi.Strip(line), ansi.Strip(expected))
				}
			}
		}
	}
}

func TestCardDigitsPreserveTinyAndLongMetadataGeometry(t *testing.T) {
	for _, compact := range []bool{false, true} {
		for width := 1; width <= 60; width++ {
			for height := 4; height <= 12; height++ {
				in := viewInput(width, height, viewSessions(1))
				in.Compact = compact
				in.Sessions[0].Profile = strings.Repeat("long-profile-", 8)
				lines := plain(renderLines(t, in))
				g := MeasureMode(width, height, compact)
				tile, ok := g.Tile(0)
				if !ok {
					continue
				}
				y, x, contentHeight := tile.Y+1, tile.X+tile.Width-1, tile.Height
				if tile.Width >= minBorderedWidth && tile.Height >= minBorderedHeight {
					y++
					x -= 1 + tilePadding
					contentHeight -= tileBorderRows
				}
				if contentHeight >= 2 && []rune(lines[y])[x] != '1' {
					t.Fatalf("compact=%v size=%dx%d lost the digit at the metadata right edge", compact, width, height)
				}
			}
		}
	}
}
