package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

type HeaderData struct {
	RunningAgents  int
	MaxAgents      int
	ThroughputTPS  float64
	RuntimeSeconds int
	TokensIn       int64
	TokensOut      int64
	TokensTotal    int64
	ModelName      string
	ProjectURL     string
	RefreshIn      int
}

type Header struct {
	width int
	data  HeaderData
}

func NewHeader() Header {
	return Header{}
}

func (h Header) Update(data HeaderData) Header {
	h.data = data
	return h
}

func (h Header) SetWidth(w int) Header {
	h.width = w
	return h
}

func (h Header) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	labelStyle := lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("244"))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("45"))
	urlStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("39"))

	line1 := titleStyle.Render("SYMPHONY STATUS")
	line2 := strings.Join([]string{
		labelStyle.Render("Agents: ") + valueStyle.Render(fmt.Sprintf("%d/%d", h.data.RunningAgents, h.data.MaxAgents)),
		labelStyle.Render("Throughput: ") + valueStyle.Render(formatThroughput(h.data.ThroughputTPS)),
		labelStyle.Render("Runtime: ") + valueStyle.Render(formatRuntime(h.data.RuntimeSeconds)),
	}, "    ")
	line3 := labelStyle.Render("Tokens: ") +
		labelStyle.Render("in: ") + valueStyle.Render(formatTokens(h.data.TokensIn)) +
		labelStyle.Render(" | out: ") + valueStyle.Render(formatTokens(h.data.TokensOut)) +
		labelStyle.Render(" | total: ") + valueStyle.Render(formatTokens(h.data.TokensTotal))
	line4 := labelStyle.Render("Model: ") + valueStyle.Render(h.data.ModelName) +
		"    " +
		labelStyle.Render("Project: ") + urlStyle.Render(h.data.ProjectURL)
	line5 := labelStyle.Render(fmt.Sprintf("Refresh in %ds", h.data.RefreshIn))

	content := strings.Join([]string{line1, line2, line3, line4, line5}, "\n")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1)
	if h.width > 0 {
		box = box.Width(h.width)
	}

	return box.Render(content)
}

func formatRuntime(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	m := seconds / 60
	s := seconds % 60
	if m == 0 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm %ds", m, s)
}

func formatTokens(n int64) string {
	negative := n < 0
	if negative {
		n = -n
	}
	raw := strconv.FormatInt(n, 10)
	if len(raw) <= 3 {
		if negative {
			return "-" + raw
		}
		return raw
	}

	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	rem := len(raw) % 3
	if rem > 0 {
		b.WriteString(raw[:rem])
		if len(raw) > rem {
			b.WriteByte(',')
		}
	}
	for i := rem; i < len(raw); i += 3 {
		b.WriteString(raw[i : i+3])
		if i+3 < len(raw) {
			b.WriteByte(',')
		}
	}
	return b.String()
}

func formatThroughput(tps float64) string {
	raw := fmt.Sprintf("%.1f", tps)
	parts := strings.SplitN(raw, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return raw + " tok/s"
	}
	return formatTokens(whole) + "." + parts[1] + " tok/s"
}
