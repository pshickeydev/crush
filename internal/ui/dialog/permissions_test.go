package dialog

import (
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

func newTestPermissions(t *testing.T) *Permissions {
	t.Helper()
	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s}
	perm := permission.PermissionRequest{
		ID:         "perm-test",
		ToolCallID: "tool-call-test",
		ToolName:   "bash",
	}
	return NewPermissions(com, perm)
}

// TestPermissions_ActionKeysResolve verifies that action keys produce the
// correct permission response.
func TestPermissions_ActionKeysResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key    tea.KeyPressMsg
		action PermissionAction
	}{
		{keyMsg('a'), PermissionAllow},
		{keyMsg('A'), PermissionAllow},
		{keyMsg('d'), PermissionDeny},
		{keyMsg('D'), PermissionDeny},
		{keyMsg('s'), PermissionAllowForSession},
		{keyMsg('S'), PermissionAllowForSession},
	}

	for _, tc := range tests {
		p := newTestPermissions(t)
		action := p.HandleMsg(tc.key)
		resp, ok := action.(ActionPermissionResponse)
		require.Truef(t, ok, "key %q should produce ActionPermissionResponse", tc.key.Text)
		require.Equal(t, tc.action, resp.Action)
	}
}

// TestPermissions_NavigationCyclesOptions verifies that tab and arrow keys
// cycle through the three permission options.
func TestPermissions_NavigationCyclesOptions(t *testing.T) {
	t.Parallel()

	p := newTestPermissions(t)
	require.Equal(t, 0, p.selectedOption)

	// Tab cycles forward.
	p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, 1, p.selectedOption)

	p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, 2, p.selectedOption)

	// Wrap around.
	p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, 0, p.selectedOption)

	// Left cycles backward.
	p.HandleMsg(keyMsg('h'))
	require.Equal(t, 2, p.selectedOption)
}

// TestPermissions_EnterConfirmsSelection verifies that enter confirms the
// currently selected option.
func TestPermissions_EnterConfirmsSelection(t *testing.T) {
	t.Parallel()

	p := newTestPermissions(t)
	p.selectedOption = 1 // Allow for session.

	action := p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	resp, ok := action.(ActionPermissionResponse)
	require.True(t, ok)
	require.Equal(t, PermissionAllowForSession, resp.Action)
}

// TestPermissions_EscapeDenies verifies that escape denies the request.
func TestPermissions_EscapeDenies(t *testing.T) {
	t.Parallel()

	p := newTestPermissions(t)
	action := p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	resp, ok := action.(ActionPermissionResponse)
	require.True(t, ok)
	require.Equal(t, PermissionDeny, resp.Action)
}

// drawPermissions draws the dialog on a screen buffer and returns it.
// The screen is large enough to avoid the forced-fullscreen path so
// the button layout is predictable.
func drawPermissions(t *testing.T, p *Permissions, w, h int) uv.ScreenBuffer {
	t.Helper()
	scr := uv.NewScreenBuffer(w, h)
	p.Draw(scr, image.Rect(0, 0, w, h))
	return scr
}

// TestPermissions_MouseClickButtons verifies that clicking each button
// with the mouse triggers the corresponding permission response. The
// click coordinates are derived from the compositor built during Draw
// so the test tracks the real screen geometry.
func TestPermissions_MouseClickButtons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		idx    int
		action PermissionAction
	}{
		{name: "Allow", idx: 0, action: PermissionAllow},
		{name: "Allow for Session", idx: 1, action: PermissionAllowForSession},
		{name: "Deny", idx: 2, action: PermissionDeny},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := newTestPermissions(t)
			drawPermissions(t, p, 120, 40)

			require.NotNil(t, p.buttonCompositor, "compositor should be built after Draw")

			// Compute a click coordinate inside the target button by
			// scanning from the button's starting X for a few columns.
			x := p.buttonScreenX
			// Advance past preceding buttons plus spacing. The button
			// width is measured from the rendered button string.
			layout := p.computeButtonLayout(p.calculateContentWidth(120), false)
			for i := 0; i < tc.idx; i++ {
				bw := lipglossWidthOfButton(p, layout.opts[i])
				x += bw + 2 // 2-space separator
			}
			clickX := x + 1 // 1 column into the button
			clickY := p.buttonScreenY

			action := p.HandleMsg(tea.MouseClickMsg(tea.Mouse{
				X:      clickX,
				Y:      clickY,
				Button: uv.MouseLeft,
			}))
			resp, ok := action.(ActionPermissionResponse)
			require.True(t, ok, "clicking %s should produce a permission response", tc.name)
			require.Equal(t, tc.action, resp.Action)
			require.Equal(t, tc.idx, p.selectedOption, "selected option should match clicked button")
		})
	}
}

// TestPermissions_MouseClickOutsideButtonsIgnored verifies that a click
// not on any button does not produce a permission response.
func TestPermissions_MouseClickOutsideButtonsIgnored(t *testing.T) {
	t.Parallel()

	p := newTestPermissions(t)
	drawPermissions(t, p, 120, 40)

	// Click in the top-left corner, well outside the centered dialog.
	action := p.HandleMsg(tea.MouseClickMsg(tea.Mouse{
		X:      0,
		Y:      0,
		Button: uv.MouseLeft,
	}))
	require.Nil(t, action, "click outside buttons should not produce an action")
}

// TestPermissions_MouseMotionSetsHover verifies that mouse motion
// updates the hover coordinates and activates mouse mode, and that the
// hovered button is reflected in the rendered button opts.
func TestPermissions_MouseMotionSetsHover(t *testing.T) {
	t.Parallel()

	p := newTestPermissions(t)
	drawPermissions(t, p, 120, 40)

	require.False(t, p.mouseActive, "mouse should be inactive before motion")

	// Move over the first button.
	clickX := p.buttonScreenX + 1
	clickY := p.buttonScreenY
	p.HandleMsg(tea.MouseMotionMsg(tea.Mouse{X: clickX, Y: clickY}))

	require.True(t, p.mouseActive, "mouse should be active after motion")
	require.Equal(t, clickX, p.hoverX)
	require.Equal(t, clickY, p.hoverY)

	// After Draw, the hovered button should be index 0.
	opts := p.applyHover(p.computeButtonLayout(p.calculateContentWidth(120), false).opts)
	require.True(t, opts[0].Hovered, "first button should be hovered")
	require.False(t, opts[1].Hovered, "second button should not be hovered")
	require.False(t, opts[2].Hovered, "third button should not be hovered")
}

// TestPermissions_KeyboardClearsMouseMode verifies that pressing a
// navigation key after mouse motion clears mouse mode so hover
// highlighting no longer applies.
func TestPermissions_KeyboardClearsMouseMode(t *testing.T) {
	t.Parallel()

	p := newTestPermissions(t)
	drawPermissions(t, p, 120, 40)

	// Activate mouse mode via motion.
	p.HandleMsg(tea.MouseMotionMsg(tea.Mouse{X: p.buttonScreenX + 1, Y: p.buttonScreenY}))
	require.True(t, p.mouseActive)

	// Pressing right should clear mouse mode and move selection.
	p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyRight})
	require.False(t, p.mouseActive, "keyboard input should clear mouse mode")
	require.Equal(t, 1, p.selectedOption)

	// Hover flags should not be set after keyboard navigation.
	opts := p.applyHover(p.computeButtonLayout(p.calculateContentWidth(120), false).opts)
	for i, o := range opts {
		require.False(t, o.Hovered, "button %d should not be hovered after keyboard nav", i)
	}
}

// lipglossWidthOfButton returns the visible width of a single rendered
// button, used to compute click offsets in tests.
func lipglossWidthOfButton(p *Permissions, opts common.ButtonOpts) int {
	return lipgloss.Width(common.Button(p.com.Styles, opts))
}

// findButtonOnScreen locates a button by its label text on the rendered
// screen and returns the screen coordinates of the first character. The
// "Allow" and "Allow for Session" buttons share a line in the non-stacked
// layout, so for "Allow" we find the first occurrence that is not part of
// the longer "Allow for Session" label.
func findButtonOnScreen(t *testing.T, scr uv.ScreenBuffer, label string) (x, y int) {
	t.Helper()
	lines := strings.Split(scr.String(), "\n")
	for i, line := range lines {
		if !strings.Contains(line, label) {
			continue
		}
		idx := strings.Index(line, label)
		// Skip "Allow" when it is the prefix of "Allow for Session".
		if label == "Allow" && idx+len(label) <= len(line) {
			rest := line[idx+len(label):]
			if strings.HasPrefix(rest, " for Session") {
				// Look for another "Allow" later on the same line.
				if next := strings.Index(line[idx+len(label):], label); next >= 0 {
					idx = idx + len(label) + next
				} else {
					continue
				}
			}
		}
		return idx, i
	}
	t.Fatalf("button %q not found on screen", label)
	return 0, 0
}

// TestPermissions_MouseClickMatchesRenderedPosition verifies that the
// hit compositor built during Draw matches the actual screen position
// of each button. This catches layout/compositor drift that would cause
// clicks to land on the wrong target or miss entirely.
func TestPermissions_MouseClickMatchesRenderedPosition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		label  string
		idx    int
		action PermissionAction
	}{
		{"Allow", 0, PermissionAllow},
		{"Allow for Session", 1, PermissionAllowForSession},
		{"Deny", 2, PermissionDeny},
	}

	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			p := newTestPermissions(t)
			scr := drawPermissions(t, p, 120, 40)

			x, y := findButtonOnScreen(t, scr, tc.label)
			action := p.HandleMsg(tea.MouseClickMsg(tea.Mouse{
				X:      x + 1,
				Y:      y,
				Button: uv.MouseLeft,
			}))
			resp, ok := action.(ActionPermissionResponse)
			require.True(t, ok, "clicking %s at rendered position should respond", tc.label)
			require.Equal(t, tc.action, resp.Action)
			require.Equal(t, tc.idx, p.selectedOption)
		})
	}
}

// TestPermissions_MouseClickEmptyContent verifies that mouse clicks work
// even when the content area is empty (e.g. MCP tools with no params).
// The button Y position must still match the rendered position.
func TestPermissions_MouseClickEmptyContent(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s}
	perm := permission.PermissionRequest{
		ID:         "perm-empty",
		ToolCallID: "tool-empty",
		ToolName:   "mcp_sometool_dothething",
	}
	p := NewPermissions(com, perm)

	scr := drawPermissions(t, p, 120, 40)
	require.NotNil(t, p.buttonCompositor)

	x, y := findButtonOnScreen(t, scr, "Allow")
	action := p.HandleMsg(tea.MouseClickMsg(tea.Mouse{
		X:      x + 1,
		Y:      y,
		Button: uv.MouseLeft,
	}))
	resp, ok := action.(ActionPermissionResponse)
	require.True(t, ok, "clicking Allow with empty content should respond")
	require.Equal(t, PermissionAllow, resp.Action)
}

// TestPermissions_MouseClickStackedButtons verifies that stacked buttons
// (narrow terminal where buttons wrap onto separate lines) can be
// clicked. Each button is center-aligned on its own line, so the
// compositor must account for per-button centering.
func TestPermissions_MouseClickStackedButtons(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s}
	perm := permission.PermissionRequest{
		ID:         "perm-stacked",
		ToolCallID: "tool-stacked",
		ToolName:   "bash",
	}

	tests := []struct {
		label  string
		idx    int
		action PermissionAction
	}{
		{"Allow", 0, PermissionAllow},
		{"Allow for Session", 1, PermissionAllowForSession},
		{"Deny", 2, PermissionDeny},
	}

	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			p := NewPermissions(com, perm)
			// Narrow screen forces buttons to stack vertically.
			scr := drawPermissions(t, p, 30, 40)

			x, y := findButtonOnScreen(t, scr, tc.label)
			action := p.HandleMsg(tea.MouseClickMsg(tea.Mouse{
				X:      x + 1,
				Y:      y,
				Button: uv.MouseLeft,
			}))
			resp, ok := action.(ActionPermissionResponse)
			require.True(t, ok, "clicking stacked %s should respond", tc.label)
			require.Equal(t, tc.action, resp.Action)
			require.Equal(t, tc.idx, p.selectedOption)
		})
	}
}

// TestPermissions_HoverDedup verifies that redundant mouse motion events
// (same X/Y) do not re-activate mouse mode after keyboard input has
// cleared it. This matches the dedup the question feature applies.
func TestPermissions_HoverDedup(t *testing.T) {
	t.Parallel()

	p := newTestPermissions(t)
	drawPermissions(t, p, 120, 40)

	// Activate mouse mode.
	p.HandleMsg(tea.MouseMotionMsg(tea.Mouse{X: p.buttonScreenX + 1, Y: p.buttonScreenY}))
	require.True(t, p.mouseActive)

	// Keyboard clears it.
	p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyRight})
	require.False(t, p.mouseActive)

	// A motion event at the SAME position should not re-activate.
	// The hover coords were last set to (buttonScreenX+1, buttonScreenY).
	p.HandleMsg(tea.MouseMotionMsg(tea.Mouse{X: p.buttonScreenX + 1, Y: p.buttonScreenY}))
	require.False(t, p.mouseActive, "redundant motion at same position should not re-activate mouse mode")

	// A motion event at a NEW position should re-activate.
	p.HandleMsg(tea.MouseMotionMsg(tea.Mouse{X: p.buttonScreenX + 2, Y: p.buttonScreenY}))
	require.True(t, p.mouseActive, "motion at new position should re-activate mouse mode")
}
