package dialog

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/fsext"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/stringext"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
)

// PermissionsID is the identifier for the permissions dialog.
const PermissionsID = "permissions"

// PermissionAction represents the user's response to a permission request.
type PermissionAction string

const (
	PermissionAllow           PermissionAction = "allow"
	PermissionAllowForSession PermissionAction = "allow_session"
	PermissionDeny            PermissionAction = "deny"
)

// Permissions dialog sizing constants.
const (
	// diffMaxWidth is the maximum width for diff views.
	diffMaxWidth = 180
	// diffSizeRatio is the size ratio for diff views relative to window.
	diffSizeRatio = 0.8
	// simpleMaxWidth is the maximum width for simple content dialogs.
	simpleMaxWidth = 100
	// simpleSizeRatio is the size ratio for simple content dialogs.
	simpleSizeRatio = 0.6
	// simpleHeightRatio is the height ratio for simple content dialogs.
	simpleHeightRatio = 0.5
	// splitModeMinWidth is the minimum width to enable split diff mode.
	splitModeMinWidth = 140
	// layoutSpacingLines is the number of empty lines used for layout spacing.
	layoutSpacingLines = 4
	// minWindowWidth is the minimum window width before forcing fullscreen.
	minWindowWidth = 77
	// minWindowHeight is the minimum window height before forcing fullscreen.
	minWindowHeight = 20
)

// Permissions represents a dialog for permission requests.
type Permissions struct {
	com          *common.Common
	windowWidth  int // Terminal window dimensions.
	windowHeight int
	fullscreen   bool // true when dialog is fullscreen

	permission     permission.PermissionRequest
	selectedOption int // 0: Allow, 1: Allow for session, 2: Deny

	viewport      viewport.Model
	viewportDirty bool // true when viewport content needs to be re-rendered
	viewportWidth int

	// Diff view state.
	diffSplitMode        *bool // nil means use default based on width
	defaultDiffSplitMode bool  // default split mode based on width
	diffXOffset          int   // horizontal scroll offset for diff view
	unifiedDiffContent   string
	splitDiffContent     string

	// Mouse state. The button hit compositor is rebuilt every Draw
	// from the current button geometry so click and hover hit-testing
	// stays in sync with the layout. mouseActive tracks whether the
	// last interaction was via the mouse so hover styling only applies
	// while the user is using the mouse.
	buttonCompositor *lipgloss.Compositor
	buttonScreenX    int
	buttonScreenY    int
	hoverX           int
	hoverY           int
	mouseActive      bool

	help   help.Model
	keyMap permissionsKeyMap
}

type permissionsKeyMap struct {
	Left             key.Binding
	Right            key.Binding
	Tab              key.Binding
	Select           key.Binding
	Allow            key.Binding
	AllowSession     key.Binding
	Deny             key.Binding
	Close            key.Binding
	ToggleDiffMode   key.Binding
	ToggleFullscreen key.Binding
	ScrollUp         key.Binding
	ScrollDown       key.Binding
	ScrollLeft       key.Binding
	ScrollRight      key.Binding
	Choose           key.Binding
	Scroll           key.Binding
}

func defaultPermissionsKeyMap() permissionsKeyMap {
	return permissionsKeyMap{
		Left: key.NewBinding(
			key.WithKeys("left", "h"),
			key.WithHelp("←", "previous"),
		),
		Right: key.NewBinding(
			key.WithKeys("right", "l"),
			key.WithHelp("→", "next"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next option"),
		),
		Select: key.NewBinding(
			key.WithKeys("enter", "ctrl+y"),
			key.WithHelp("enter", "confirm"),
		),
		Allow: key.NewBinding(
			key.WithKeys("a", "A", "ctrl+a"),
			key.WithHelp("a", "allow"),
		),
		AllowSession: key.NewBinding(
			key.WithKeys("s", "S", "ctrl+s"),
			key.WithHelp("s", "allow session"),
		),
		Deny: key.NewBinding(
			key.WithKeys("d", "D"),
			key.WithHelp("d", "deny"),
		),
		Close: CloseKey,
		ToggleDiffMode: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "toggle diff view"),
		),
		ToggleFullscreen: key.NewBinding(
			key.WithKeys("f"),
			key.WithHelp("f", "toggle fullscreen"),
		),
		ScrollUp: key.NewBinding(
			key.WithKeys("shift+up", "K"),
			key.WithHelp("shift+↑", "scroll up"),
		),
		ScrollDown: key.NewBinding(
			key.WithKeys("shift+down", "J"),
			key.WithHelp("shift+↓", "scroll down"),
		),
		ScrollLeft: key.NewBinding(
			key.WithKeys("shift+left", "H"),
			key.WithHelp("shift+←", "scroll left"),
		),
		ScrollRight: key.NewBinding(
			key.WithKeys("shift+right", "L"),
			key.WithHelp("shift+→", "scroll right"),
		),
		Choose: key.NewBinding(
			key.WithKeys("left", "right"),
			key.WithHelp("←/→", "choose"),
		),
		Scroll: key.NewBinding(
			key.WithKeys("shift+left", "shift+down", "shift+up", "shift+right"),
			key.WithHelp("shift+←↓↑→", "scroll"),
		),
	}
}

var _ Dialog = (*Permissions)(nil)

// PermissionsOption configures the permissions dialog.
type PermissionsOption func(*Permissions)

// WithDiffMode sets the initial diff mode (split or unified).
func WithDiffMode(split bool) PermissionsOption {
	return func(p *Permissions) {
		p.diffSplitMode = &split
	}
}

// NewPermissions creates a new permissions dialog.
func NewPermissions(com *common.Common, perm permission.PermissionRequest, opts ...PermissionsOption) *Permissions {
	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()

	km := defaultPermissionsKeyMap()

	// Configure viewport with matching keybindings.
	vp := viewport.New()
	vp.KeyMap = viewport.KeyMap{
		Up:    km.ScrollUp,
		Down:  km.ScrollDown,
		Left:  km.ScrollLeft,
		Right: km.ScrollRight,
		// Disable other viewport keys to avoid conflicts with dialog shortcuts.
		PageUp:       key.NewBinding(key.WithDisabled()),
		PageDown:     key.NewBinding(key.WithDisabled()),
		HalfPageUp:   key.NewBinding(key.WithDisabled()),
		HalfPageDown: key.NewBinding(key.WithDisabled()),
	}

	p := &Permissions{
		com:            com,
		permission:     perm,
		selectedOption: 0,
		viewport:       vp,
		help:           h,
		keyMap:         km,
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

// Calculate usable content width (dialog border + horizontal padding).
func (p *Permissions) calculateContentWidth(width int) int {
	t := p.com.Styles
	const dialogHorizontalPadding = 2
	return width - t.Dialog.View.GetHorizontalFrameSize() - dialogHorizontalPadding
}

// ID implements [Dialog].
func (*Permissions) ID() string {
	return PermissionsID
}

// ToolCallID returns the tool call ID associated with this dialog's
// permission request.
func (p *Permissions) ToolCallID() string {
	return p.permission.ToolCallID
}

// HandleMsg implements [Dialog].
func (p *Permissions) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		// Keyboard navigation takes over from mouse hover mode so the
		// selected highlight follows the keyboard cursor, not the last
		// hovered button.
		p.mouseActive = false
		switch {
		case key.Matches(msg, p.keyMap.Close):
			// Escape denies the permission request.
			return p.respond(PermissionDeny)
		case key.Matches(msg, p.keyMap.Right), key.Matches(msg, p.keyMap.Tab):
			p.selectedOption = (p.selectedOption + 1) % 3
		case key.Matches(msg, p.keyMap.Left):
			// Add 2 instead of subtracting 1 to avoid negative modulo.
			p.selectedOption = (p.selectedOption + 2) % 3
		case key.Matches(msg, p.keyMap.Select):
			return p.selectCurrentOption()
		case key.Matches(msg, p.keyMap.Allow):
			return p.respond(PermissionAllow)
		case key.Matches(msg, p.keyMap.AllowSession):
			return p.respond(PermissionAllowForSession)
		case key.Matches(msg, p.keyMap.Deny):
			return p.respond(PermissionDeny)
		case key.Matches(msg, p.keyMap.ToggleDiffMode):
			if p.hasDiffView() {
				newMode := !p.isSplitMode()
				p.diffSplitMode = &newMode
				p.viewportDirty = true
			}
		case key.Matches(msg, p.keyMap.ToggleFullscreen):
			if p.hasDiffView() {
				p.fullscreen = !p.fullscreen
			}
		case key.Matches(msg, p.keyMap.ScrollDown):
			p.viewport, _ = p.viewport.Update(msg)
		case key.Matches(msg, p.keyMap.ScrollUp):
			p.viewport, _ = p.viewport.Update(msg)
		case key.Matches(msg, p.keyMap.ScrollLeft):
			if p.hasDiffView() {
				p.scrollLeft()
			} else {
				p.viewport, _ = p.viewport.Update(msg)
			}
		case key.Matches(msg, p.keyMap.ScrollRight):
			if p.hasDiffView() {
				p.scrollRight()
			} else {
				p.viewport, _ = p.viewport.Update(msg)
			}
		}
	case tea.MouseClickMsg:
		return p.handleMouseClick(msg)
	case tea.MouseMotionMsg:
		p.handleMouseMotion(msg)
	case common.CoalescedWheelMsg:
		if p.hasDiffView() {
			if msg.DeltaX < 0 {
				p.scrollLeft()
			} else if msg.DeltaX > 0 {
				p.scrollRight()
			} else {
				p.viewport, _ = p.viewport.Update(tea.MouseWheelMsg(msg.Mouse))
			}
		} else {
			p.viewport, _ = p.viewport.Update(tea.MouseWheelMsg(msg.Mouse))
		}
	default:
		// Pass unhandled keys to viewport for non-diff content scrolling.
		if !p.hasDiffView() {
			p.viewport, _ = p.viewport.Update(msg)
			p.viewportDirty = true
		}
	}

	return nil
}

// handleMouseClick processes a mouse click. If the click lands on a
// button, that button is selected and its action is immediately
// triggered, matching the behavior of pressing Enter on the keyboard
// selection. Clicks outside the buttons are ignored.
func (p *Permissions) handleMouseClick(msg tea.MouseClickMsg) Action {
	idx := common.HitButtonIndex(p.buttonCompositor, msg.X, msg.Y)
	if idx < 0 {
		return nil
	}
	p.selectedOption = idx
	return p.selectCurrentOption()
}

// handleMouseMotion updates the hover position so the next Draw can
// highlight the button under the cursor. Any mouse motion activates
// hover mode; keyboard input clears it. Redundant motion events (same
// X/Y) are ignored to match the dedup the question feature applies in
// ui.go.
func (p *Permissions) handleMouseMotion(msg tea.MouseMotionMsg) {
	if p.hoverX == msg.X && p.hoverY == msg.Y {
		return
	}
	p.hoverX = msg.X
	p.hoverY = msg.Y
	p.mouseActive = true
}

func (p *Permissions) selectCurrentOption() tea.Msg {
	switch p.selectedOption {
	case 0:
		return p.respond(PermissionAllow)
	case 1:
		return p.respond(PermissionAllowForSession)
	default:
		return p.respond(PermissionDeny)
	}
}

func (p *Permissions) respond(action PermissionAction) tea.Msg {
	return ActionPermissionResponse{
		Permission: p.permission,
		Action:     action,
	}
}

func (p *Permissions) hasDiffView() bool {
	switch p.permission.ToolName {
	case tools.EditToolName, tools.WriteToolName, tools.MultiEditToolName, tools.ReplaceSymbolToolName:
		return true
	}
	return false
}

func (p *Permissions) isSplitMode() bool {
	if p.diffSplitMode != nil {
		return *p.diffSplitMode
	}
	return p.defaultDiffSplitMode
}

const horizontalScrollStep = 5

func (p *Permissions) scrollLeft() {
	p.diffXOffset = max(0, p.diffXOffset-horizontalScrollStep)
	p.viewportDirty = true
}

func (p *Permissions) scrollRight() {
	p.diffXOffset += horizontalScrollStep
	p.viewportDirty = true
}

// Draw implements [Dialog].
// dialogSize returns the outer dialog width and maximum height for the
// given screen area, and whether the window is too small so the dialog
// must fill it. Diff views get more room than simple prompts.
func (p *Permissions) dialogSize(area uv.Rectangle) (width, maxHeight int, forceFullscreen bool) {
	forceFullscreen = area.Dx() <= minWindowWidth || area.Dy() <= minWindowHeight
	switch {
	case forceFullscreen || (p.fullscreen && p.hasDiffView()):
		width, maxHeight = area.Dx(), area.Dy()
	case p.hasDiffView():
		// Wide for side-by-side diffs, capped for readability.
		width = min(int(float64(area.Dx())*diffSizeRatio), diffMaxWidth)
		maxHeight = int(float64(area.Dy()) * diffSizeRatio)
	default:
		// Narrower for simple content like commands and URLs.
		width = min(int(float64(area.Dx())*simpleSizeRatio), simpleMaxWidth)
		maxHeight = int(float64(area.Dy()) * simpleHeightRatio)
	}
	return width, maxHeight, forceFullscreen
}

// contentViewportHeight returns the height for the scrollable content
// viewport given the height taken by the fixed chrome (fixedHeight) and
// the content's natural height. Simple prompts shrink to fit their
// content; diff and fullscreen views always take all remaining height.
func (p *Permissions) contentViewportHeight(forceFullscreen bool, maxHeight, fixedHeight, contentHeight int) int {
	if p.hasDiffView() || forceFullscreen {
		return maxHeight - fixedHeight
	}
	if fixedHeight+contentHeight < maxHeight {
		return max(contentHeight, 3)
	}
	return max(maxHeight-fixedHeight, 3)
}

func (p *Permissions) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := p.com.Styles
	width, maxHeight, forceFullscreen := p.dialogSize(area)
	dialogStyle := t.Dialog.View.Width(width).Padding(0, 1)
	// The dialog fills the screen when forced small or when a diff is
	// expanded; center the buttons then instead of hugging the far edge.
	fullscreen := forceFullscreen || (p.fullscreen && p.hasDiffView())

	contentWidth := p.calculateContentWidth(width)
	header := p.renderHeader(contentWidth)
	// Compute button layout once (without hover) to measure geometry.
	btnLayout := p.computeButtonLayout(contentWidth, fullscreen)
	buttonsHeight := lipgloss.Height(btnLayout.content)
	// Pack the hints to the content width so they truncate cleanly instead
	// of overflowing. The dialog frame supplies the padding, so this renders
	// the hint line without the extra help view inset that renderDialogHelp
	// applies for RenderContext dialogs.
	helpView := shortHelpLine(&p.help, p.ShortHelp(), contentWidth)

	p.defaultDiffSplitMode = width >= splitModeMinWidth

	// Pre-render content to measure its actual height, then fit the
	// scrollable viewport into whatever height the fixed chrome leaves.
	renderedContent := p.renderContent(contentWidth)
	contentHeight := lipgloss.Height(renderedContent)
	fixedHeight := lipgloss.Height(header) + buttonsHeight +
		lipgloss.Height(helpView) + dialogStyle.GetVerticalFrameSize() + layoutSpacingLines
	availableHeight := p.contentViewportHeight(forceFullscreen, maxHeight, fixedHeight, contentHeight)

	// Determine if scrollbar is needed.
	needsScrollbar := p.hasDiffView() || contentHeight > availableHeight
	viewportWidth := contentWidth
	if needsScrollbar {
		viewportWidth = contentWidth - 1 // Reserve space for scrollbar.
	}

	if p.viewport.Width() != viewportWidth {
		// Mark content as dirty if width has changed.
		p.viewportDirty = true
		renderedContent = p.renderContent(viewportWidth)
	}

	var content string
	p.viewport.SetWidth(viewportWidth)
	p.viewport.SetHeight(availableHeight)
	if p.viewportDirty {
		p.viewport.SetContent(renderedContent)
		p.viewportWidth = p.viewport.Width()
		p.viewportDirty = false
	}
	content = p.viewport.View()
	if needsScrollbar {
		content = joinScrollbar(t, content, availableHeight, p.viewport.TotalLineCount(), availableHeight, p.viewport.YOffset())
	}

	parts := []string{header}
	if content != "" {
		parts = append(parts, "", content)
	}
	parts = append(parts, "", btnLayout.content, "", helpView)

	innerContent := lipgloss.JoinVertical(lipgloss.Left, parts...)
	rendered := dialogStyle.Render(innerContent)

	// Compute the centered rect so we can place the button hit
	// compositor at the correct screen coordinates. This mirrors
	// DrawCenterCursor but keeps the rect for hit-testing.
	dialogW, dialogH := lipgloss.Size(rendered)
	dialogW = min(dialogW, area.Dx())
	dialogH = min(dialogH, area.Dy())
	dialogRect := common.CenterRect(area, dialogW, dialogH)

	// Compute the button row's screen position inside the dialog.
	// The dialog frame (border + padding) sits above the content;
	// the buttons follow header + blank + viewport content + blank.
	hFrame := dialogStyle.GetHorizontalFrameSize() // border + padding on one side
	vFrame := dialogStyle.GetVerticalFrameSize()
	contentLeft := dialogRect.Min.X + hFrame/2
	contentTop := dialogRect.Min.Y + vFrame/2
	buttonY := contentTop + lipgloss.Height(header) + 1 + availableHeight + 1

	// Determine the button group's X position based on alignment.
	var buttonX int
	switch btnLayout.align {
	case lipgloss.Center:
		buttonX = contentLeft + (contentWidth-btnLayout.groupWidth)/2
	default: // Right
		buttonX = contentLeft + contentWidth - btnLayout.groupWidth
	}
	if buttonX < contentLeft {
		buttonX = contentLeft
	}

	// Build the hit compositor at the computed screen coordinates,
	// then re-render buttons with hover styling applied.
	p.buttonCompositor = p.buildButtonCompositor(btnLayout, buttonX, buttonY, contentWidth)
	p.buttonScreenX = buttonX
	p.buttonScreenY = buttonY
	buttons := p.renderButtons(contentWidth, fullscreen)

	// Reassemble with the hover-styled buttons.
	parts = []string{header}
	if content != "" {
		parts = append(parts, "", content)
	}
	parts = append(parts, "", buttons, "", helpView)
	innerContent = lipgloss.JoinVertical(lipgloss.Left, parts...)
	DrawCenterCursor(scr, area, dialogStyle.Render(innerContent), nil)
	return nil
}

func (p *Permissions) renderHeader(contentWidth int) string {
	t := p.com.Styles

	title := common.DialogTitle(t, "Permission Required", contentWidth-t.Dialog.Title.GetHorizontalFrameSize(), t.Dialog.TitleGradFromColor, t.Dialog.TitleGradToColor)
	title = t.Dialog.Title.Render(title)

	// Tool info.
	toolLine := p.renderToolName(contentWidth)

	lines := []string{title, "", toolLine}

	// Show generic Path only for tools that don't render their own file/path line.
	switch p.permission.ToolName {
	case tools.EditToolName, tools.WriteToolName, tools.MultiEditToolName,
		tools.ViewToolName, tools.ReplaceSymbolToolName,
		tools.DownloadToolName, tools.LSToolName:
		// These tools show their own File/Directory line below.
	default:
		lines = append(lines, p.renderKeyValue("Path", fsext.PrettyPath(p.permission.Path), contentWidth))
	}

	// Add tool-specific header info.
	switch p.permission.ToolName {
	case tools.BashToolName:
		if params, ok := p.permission.Params.(tools.BashPermissionsParams); ok {
			lines = append(lines, p.renderKeyValue("Desc", params.Description, contentWidth))
		}
	case tools.DownloadToolName:
		if params, ok := p.permission.Params.(tools.DownloadPermissionsParams); ok {
			lines = append(lines, p.renderKeyValue("URL", params.URL, contentWidth))
			lines = append(lines, p.renderKeyValue("File", fsext.PrettyPath(params.FilePath), contentWidth))
		}
	case tools.EditToolName, tools.WriteToolName, tools.MultiEditToolName, tools.ViewToolName, tools.ReplaceSymbolToolName:
		var filePath string
		switch params := p.permission.Params.(type) {
		case tools.EditPermissionsParams:
			filePath = params.FilePath
		case tools.WritePermissionsParams:
			filePath = params.FilePath
		case tools.MultiEditPermissionsParams:
			filePath = params.FilePath
		case tools.ViewPermissionsParams:
			filePath = params.FilePath
		case tools.ReplaceSymbolPermissionsParams:
			filePath = params.FilePath
		}
		if filePath != "" {
			lines = append(lines, p.renderKeyValue("File", fsext.PrettyPath(filePath), contentWidth))
		}
	case tools.LSToolName:
		if params, ok := p.permission.Params.(tools.LSPermissionsParams); ok {
			lines = append(lines, p.renderKeyValue("Directory", fsext.PrettyPath(params.Path), contentWidth))
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (p *Permissions) renderKeyValue(key, value string, width int) string {
	t := p.com.Styles
	keyStyle := t.Dialog.Permissions.KeyText
	valueStyle := t.Dialog.Permissions.ValueText

	keyStr := keyStyle.Render(key)
	valueStr := valueStyle.Width(width - lipgloss.Width(keyStr) - 1).Render(" " + value)

	return lipgloss.JoinHorizontal(lipgloss.Left, keyStr, valueStr)
}

func (p *Permissions) renderToolName(width int) string {
	toolName := p.permission.ToolName

	// Check if this is an MCP tool (format: mcp_<mcpname>_<toolname>).
	if strings.HasPrefix(toolName, "mcp_") {
		parts := strings.SplitN(toolName, "_", 3)
		if len(parts) == 3 {
			mcpName := prettyName(parts[1])
			toolPart := prettyName(parts[2])
			toolName = fmt.Sprintf("%s %s %s", mcpName, styles.ArrowRightIcon, toolPart)
		}
	}

	return p.renderKeyValue("Tool", toolName, width)
}

// prettyName converts snake_case or kebab-case to Title Case.
func prettyName(name string) string {
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, "-", " ")
	return stringext.Capitalize(name)
}

func (p *Permissions) renderContent(width int) string {
	switch p.permission.ToolName {
	case tools.BashToolName:
		return p.renderBashContent(width)
	case tools.EditToolName:
		return p.renderEditContent(width)
	case tools.WriteToolName:
		return p.renderWriteContent(width)
	case tools.MultiEditToolName:
		return p.renderMultiEditContent(width)
	case tools.ReplaceSymbolToolName:
		return p.renderReplaceSymbolContent(width)
	case tools.DownloadToolName:
		return p.renderDownloadContent(width)
	case tools.FetchToolName:
		return p.renderFetchContent(width)
	case tools.AgenticFetchToolName:
		return p.renderAgenticFetchContent(width)
	case tools.ViewToolName:
		return p.renderViewContent(width)
	case tools.LSToolName:
		return p.renderLSContent(width)
	default:
		return p.renderDefaultContent(width)
	}
}

func (p *Permissions) renderBashContent(width int) string {
	params, ok := p.permission.Params.(tools.BashPermissionsParams)
	if !ok {
		return ""
	}

	return p.renderContentPanel(params.Command, width)
}

func (p *Permissions) renderEditContent(contentWidth int) string {
	params, ok := p.permission.Params.(tools.EditPermissionsParams)
	if !ok {
		return ""
	}
	return p.renderDiff(params.FilePath, params.OldContent, params.NewContent, contentWidth)
}

func (p *Permissions) renderWriteContent(contentWidth int) string {
	params, ok := p.permission.Params.(tools.WritePermissionsParams)
	if !ok {
		return ""
	}
	return p.renderDiff(params.FilePath, params.OldContent, params.NewContent, contentWidth)
}

func (p *Permissions) renderMultiEditContent(contentWidth int) string {
	params, ok := p.permission.Params.(tools.MultiEditPermissionsParams)
	if !ok {
		return ""
	}
	return p.renderDiff(params.FilePath, params.OldContent, params.NewContent, contentWidth)
}

func (p *Permissions) renderReplaceSymbolContent(contentWidth int) string {
	params, ok := p.permission.Params.(tools.ReplaceSymbolPermissionsParams)
	if !ok {
		return ""
	}
	return p.renderDiff(params.FilePath, params.OldContent, params.NewContent, contentWidth)
}

func (p *Permissions) renderDiff(filePath, oldContent, newContent string, contentWidth int) string {
	if !p.viewportDirty {
		if p.isSplitMode() {
			return p.splitDiffContent
		}
		return p.unifiedDiffContent
	}

	isSplitMode := p.isSplitMode()
	formatter := common.DiffFormatter(p.com.Styles).
		Before(fsext.PrettyPath(filePath), oldContent).
		After(fsext.PrettyPath(filePath), newContent).
		XOffset(p.diffXOffset).
		Width(contentWidth)

	var result string
	if isSplitMode {
		formatter = formatter.Split()
		p.splitDiffContent = formatter.String()
		result = p.splitDiffContent
	} else {
		formatter = formatter.Unified()
		p.unifiedDiffContent = formatter.String()
		result = p.unifiedDiffContent
	}

	return result
}

func (p *Permissions) renderDownloadContent(width int) string {
	params, ok := p.permission.Params.(tools.DownloadPermissionsParams)
	if !ok {
		return ""
	}

	content := fmt.Sprintf("URL: %s\nFile: %s", params.URL, fsext.PrettyPath(params.FilePath))
	if params.Timeout > 0 {
		content += fmt.Sprintf("\nTimeout: %ds", params.Timeout)
	}

	return p.renderContentPanel(content, width)
}

func (p *Permissions) renderFetchContent(width int) string {
	params, ok := p.permission.Params.(tools.FetchPermissionsParams)
	if !ok {
		return ""
	}

	return p.renderContentPanel(params.URL, width)
}

func (p *Permissions) renderAgenticFetchContent(width int) string {
	params, ok := p.permission.Params.(tools.AgenticFetchPermissionsParams)
	if !ok {
		return ""
	}

	var content string
	if params.URL != "" {
		content = fmt.Sprintf("URL: %s\n\nPrompt: %s", params.URL, params.Prompt)
	} else {
		content = fmt.Sprintf("Prompt: %s", params.Prompt)
	}

	return p.renderContentPanel(content, width)
}

func (p *Permissions) renderViewContent(width int) string {
	params, ok := p.permission.Params.(tools.ViewPermissionsParams)
	if !ok {
		return ""
	}

	content := fmt.Sprintf("File: %s", fsext.PrettyPath(params.FilePath))
	if params.Offset > 0 {
		content += fmt.Sprintf("\nStarting from line: %d", params.Offset+1)
	}
	if params.Limit > 0 && params.Limit != 2000 {
		content += fmt.Sprintf("\nLines to read: %d", params.Limit)
	}

	return p.renderContentPanel(content, width)
}

func (p *Permissions) renderLSContent(width int) string {
	params, ok := p.permission.Params.(tools.LSPermissionsParams)
	if !ok {
		return ""
	}

	content := fmt.Sprintf("Directory: %s", fsext.PrettyPath(params.Path))
	if len(params.Ignore) > 0 {
		content += fmt.Sprintf("\nIgnore patterns: %s", strings.Join(params.Ignore, ", "))
	}

	return p.renderContentPanel(content, width)
}

func (p *Permissions) renderDefaultContent(width int) string {
	t := p.com.Styles
	var content string
	// do not add the description for mcp tools
	if !strings.HasPrefix(p.permission.ToolName, "mcp_") {
		content = p.permission.Description
	}

	// Pretty-print JSON params if available.
	if p.permission.Params != nil {
		var paramStr string
		if str, ok := p.permission.Params.(string); ok {
			paramStr = str
		} else {
			paramStr = fmt.Sprintf("%v", p.permission.Params)
		}

		var parsed any
		if err := json.Unmarshal([]byte(paramStr), &parsed); err == nil {
			if b, err := json.MarshalIndent(parsed, "", "  "); err == nil {
				jsonContent := string(b)
				highlighted, err := common.SyntaxHighlight(t, jsonContent, "params.json", t.Dialog.Permissions.ParamsBg)
				if err == nil {
					jsonContent = highlighted
				}
				if content != "" {
					content += "\n\n"
				}
				content += jsonContent
			}
		} else if paramStr != "" {
			if content != "" {
				content += "\n\n"
			}
			content += paramStr
		}
	}

	if content == "" {
		return ""
	}

	return p.renderContentPanel(strings.TrimSpace(content), width)
}

// renderContentPanel renders content in a panel with the full width.
func (p *Permissions) renderContentPanel(content string, width int) string {
	panelStyle := p.com.Styles.Dialog.ContentPanel
	return panelStyle.Width(width).Render(content)
}

// buttonLayout holds the geometry of the rendered button group. It
// is computed once per Draw so the hit compositor can be built at the
// correct screen coordinates and hover styling can be applied before
// the final render.
type buttonLayout struct {
	opts       []common.ButtonOpts
	spacing    string // separator between buttons ("  " or "\n")
	stacked    bool   // true when buttons wrap onto separate lines
	align      lipgloss.Position
	groupWidth int // visible width of the button group (excluding alignment padding)
	content    string
}

// computeButtonLayout builds the button opts and rendered group for the
// current selection state, without applying hover styling. The caller
// applies hover via applyHover before the final render.
func (p *Permissions) computeButtonLayout(contentWidth int, fullscreen bool) buttonLayout {
	opts := []common.ButtonOpts{
		{Text: "Allow", UnderlineIndex: 0, Selected: p.selectedOption == 0},
		{Text: "Allow for Session", UnderlineIndex: 10, Selected: p.selectedOption == 1},
		{Text: "Deny", UnderlineIndex: 0, Selected: p.selectedOption == 2},
	}

	spacing := "  "
	content := common.ButtonGroup(p.com.Styles, opts, spacing)
	align := lipgloss.Right
	if fullscreen {
		align = lipgloss.Center
	}
	stacked := false
	if lipgloss.Width(content) > contentWidth {
		spacing = "\n"
		stacked = true
		align = lipgloss.Center
		content = common.ButtonGroup(p.com.Styles, opts, spacing)
	}

	return buttonLayout{
		opts:       opts,
		spacing:    spacing,
		stacked:    stacked,
		align:      align,
		groupWidth: lipgloss.Width(content),
		content:    content,
	}
}

// applyHover returns a new slice of button opts with Hovered flags set
// based on the current hover position and the provided compositor. When
// the mouse is not active (last interaction was via keyboard), no hover
// flags are set.
func (p *Permissions) applyHover(opts []common.ButtonOpts) []common.ButtonOpts {
	out := make([]common.ButtonOpts, len(opts))
	copy(out, opts)
	if !p.mouseActive {
		return out
	}
	hovered := common.HitButtonIndex(p.buttonCompositor, p.hoverX, p.hoverY)
	for i := range out {
		out[i].Hovered = i == hovered
	}
	return out
}

// buildButtonCompositor constructs the hit-test compositor for the
// button group at the given screen coordinates. For stacked buttons
// each button is placed on its own line, centered within contentWidth.
func (p *Permissions) buildButtonCompositor(layout buttonLayout, x, y, contentWidth int) *lipgloss.Compositor {
	if layout.stacked {
		return p.buildStackedButtonCompositor(layout, x, y, contentWidth)
	}
	return common.ButtonHitCompositor(p.com.Styles, layout.opts, layout.spacing, x, y)
}

// buildStackedButtonCompositor builds a compositor where each button
// is on its own line, centered within contentWidth to match the
// center alignment applied by renderButtons in the stacked case.
func (p *Permissions) buildStackedButtonCompositor(layout buttonLayout, x, y, contentWidth int) *lipgloss.Compositor {
	if len(layout.opts) == 0 {
		return nil
	}
	var layers []*lipgloss.Layer
	for i, o := range layout.opts {
		b := common.Button(p.com.Styles, o)
		w := lipgloss.Width(b)
		// Center each button within contentWidth, matching the
		// lipgloss.Center alignment applied by renderButtons.
		btnX := x + (contentWidth-w)/2
		if btnX < x {
			btnX = x
		}
		hitStr := strings.Repeat(" ", w)
		layers = append(layers, lipgloss.NewLayer(hitStr).X(btnX).Y(y+i).ID(fmt.Sprintf("btn_%d", i)))
	}
	return lipgloss.NewCompositor(layers...)
}

func (p *Permissions) renderButtons(contentWidth int, fullscreen bool) string {
	layout := p.computeButtonLayout(contentWidth, fullscreen)
	opts := p.applyHover(layout.opts)
	content := common.ButtonGroup(p.com.Styles, opts, layout.spacing)

	return lipgloss.NewStyle().
		Width(contentWidth).
		Align(layout.align).
		Render(content)
}

func (p *Permissions) canScroll() bool {
	if p.hasDiffView() {
		// Diff views can always scroll.
		return true
	}
	// For non-diff content, check if viewport has scrollable content.
	return !p.viewport.AtTop() || !p.viewport.AtBottom()
}

// ShortHelp implements [help.KeyMap].
func (p *Permissions) ShortHelp() []key.Binding {
	bindings := []key.Binding{
		p.keyMap.Choose,
		p.keyMap.Select,
		p.keyMap.Close,
	}

	if p.canScroll() {
		bindings = append(bindings, p.keyMap.Scroll)
	}

	if p.hasDiffView() {
		bindings = append(
			bindings,
			p.keyMap.ToggleDiffMode,
			p.keyMap.ToggleFullscreen,
		)
	}

	return bindings
}

// FullHelp implements [help.KeyMap].
func (p *Permissions) FullHelp() [][]key.Binding {
	return [][]key.Binding{p.ShortHelp()}
}
