package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chzyer/readline"
	"github.com/manifoldco/promptui"

	"infermeshai/internal/model"
)

type ChatOptions struct {
	BaseURL string
	Model   string
}

type ChatRunner struct {
	opts       ChatOptions
	httpClient *http.Client
}

type sendStats struct {
	TotalLatency      time.Duration
	FirstTokenLatency time.Duration
	CharsStreamed     int
}

type modelChoice struct {
	Name      string
	Providers []model.CapabilityDocument
}

func NewChatRunner(opts ChatOptions) *ChatRunner {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = "http://127.0.0.1:5555"
	}
	modelName := strings.TrimSpace(opts.Model)
	if modelName == "" {
		modelName = "llama3-8b"
	}
	return &ChatRunner{
		opts: ChatOptions{
			BaseURL: base,
			Model:   modelName,
		},
		httpClient: &http.Client{},
	}
}

func (c *ChatRunner) SendPrompt(ctx context.Context, prompt string, out io.Writer) error {
	_, err := c.sendPromptWithStats(ctx, prompt, out)
	return err
}

func (c *ChatRunner) sendPromptWithStats(ctx context.Context, prompt string, out io.Writer) (sendStats, error) {
	start := time.Now()
	body, _ := json.Marshal(model.ChatCompletionRequest{
		Model: c.opts.Model,
		Messages: []model.Message{
			{Role: "user", Content: prompt},
		},
		Stream: true,
	})
	u, err := url.JoinPath(c.opts.BaseURL, "/v1/chat/completions")
	if err != nil {
		return sendStats{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return sendStats{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return sendStats{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return sendStats{}, fmt.Errorf("chat request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	stats := sendStats{}
	firstTokenSeen := false
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			fmt.Fprintln(out)
			stats.TotalLatency = time.Since(start)
			return stats, nil
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				if !firstTokenSeen {
					firstTokenSeen = true
					stats.FirstTokenLatency = time.Since(start)
				}
				stats.CharsStreamed += len(ch.Delta.Content)
				_, _ = io.WriteString(out, ch.Delta.Content)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return sendStats{}, err
	}
	fmt.Fprintln(out)
	stats.TotalLatency = time.Since(start)
	return stats, nil
}

func (c *ChatRunner) RunREPL(ctx context.Context, in io.Reader, out, errOut io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	c.printHelp(out)
	for {
		_, _ = io.WriteString(out, "> ")
		if !sc.Scan() {
			return nil
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			quit, err := c.handleCommand(ctx, line, sc, out, errOut)
			if err != nil {
				return err
			}
			if quit {
				return nil
			}
			continue
		}
		_, _ = fmt.Fprintf(out, "[sending model=%s]\n", c.opts.Model)
		stats, err := c.sendPromptWithStats(ctx, line, out)
		if err != nil {
			_, _ = fmt.Fprintf(errOut, "chat error: %v\n", err)
			continue
		}
		_, _ = fmt.Fprintf(out, "[done total=%s first_token=%s chars=%d]\n", stats.TotalLatency.Truncate(time.Millisecond), stats.FirstTokenLatency.Truncate(time.Millisecond), stats.CharsStreamed)
	}
}

func (c *ChatRunner) RunInteractive(ctx context.Context, out, errOut io.Writer) error {
	historyFile := chatHistoryPath()
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "> ",
		HistoryFile:     historyFile,
		InterruptPrompt: "^C",
		EOFPrompt:       "/quit",
		AutoComplete:    c.commandCompleter(),
	})
	if err != nil {
		return err
	}
	defer rl.Close()

	c.printHelp(out)
	for {
		line, err := rl.Readline()
		if err == readline.ErrInterrupt {
			_, _ = io.WriteString(out, "\n")
			continue
		}
		if err == io.EOF {
			return nil
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			quit, hErr := c.handleInteractiveCommand(ctx, line, out, errOut)
			if hErr != nil {
				return hErr
			}
			if quit {
				return nil
			}
			continue
		}
		_, _ = fmt.Fprintf(out, "[sending model=%s]\n", c.opts.Model)
		stats, sendErr := c.sendPromptWithStats(ctx, line, out)
		if sendErr != nil {
			_, _ = fmt.Fprintf(errOut, "chat error: %v\n", sendErr)
			continue
		}
		_, _ = fmt.Fprintf(out, "[done total=%s first_token=%s chars=%d]\n", stats.TotalLatency.Truncate(time.Millisecond), stats.FirstTokenLatency.Truncate(time.Millisecond), stats.CharsStreamed)
	}
}

func chatHistoryPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ".infermeshai_chat_history"
	}
	dir := filepath.Join(home, ".infermeshai")
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "chat.history")
}

func (c *ChatRunner) commandCompleter() *readline.PrefixCompleter {
	return readline.NewPrefixCompleter(
		readline.PcItem("/help"),
		readline.PcItem("/"),
		readline.PcItem("/status"),
		readline.PcItem("/current"),
		readline.PcItem("/quit"),
		readline.PcItem("/exit"),
		readline.PcItem("/peers"),
		readline.PcItem("/models"),
		readline.PcItem("/model"),
		readline.PcItem("/use"),
		readline.PcItem("/routing",
			readline.PcItem("auto"),
			readline.PcItem("manual"),
			readline.PcItem("prefer_known"),
			readline.PcItem("prefer_favorites"),
		),
	)
}

func (c *ChatRunner) handleInteractiveCommand(ctx context.Context, line string, out, errOut io.Writer) (bool, error) {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return false, nil
	}
	switch parts[0] {
	case "/":
		c.printSlashMenu(out)
		return false, nil
	case "/help", "/?":
		c.printHelp(out)
		return false, nil
	case "/status", "/current":
		return false, c.printStatus(ctx, out)
	case "/quit", "/exit":
		return true, nil
	case "/models":
		return false, c.printModels(ctx, out)
	case "/model":
		if len(parts) < 2 {
			return false, c.pickModelArrow(ctx, out)
		}
		return false, c.selectModel(ctx, strings.TrimSpace(parts[1]), nil, out, errOut)
	case "/peers":
		return false, c.printPeers(ctx, out)
	case "/use":
		if len(parts) < 2 {
			return false, c.pickPeerArrow(ctx, out, errOut)
		}
		return false, c.selectPeerByArg(ctx, parts[1], nil, out, errOut, true)
	case "/routing":
		if len(parts) < 2 {
			_, _ = io.WriteString(errOut, "usage: /routing <auto|manual|prefer_known|prefer_favorites>\n")
			return false, nil
		}
		return false, c.setRouting(ctx, parts[1], out)
	default:
		_, _ = fmt.Fprintf(errOut, "unknown command: %s (use /help)\n", parts[0])
		return false, nil
	}
}

func (c *ChatRunner) handleCommand(ctx context.Context, line string, sc *bufio.Scanner, out, errOut io.Writer) (bool, error) {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return false, nil
	}
	switch parts[0] {
	case "/":
		c.printSlashMenu(out)
		return false, nil
	case "/help", "/?":
		c.printHelp(out)
		return false, nil
	case "/status", "/current":
		return false, c.printStatus(ctx, out)
	case "/quit", "/exit":
		return true, nil
	case "/models":
		return false, c.printModels(ctx, out)
	case "/model":
		if len(parts) < 2 {
			return false, c.pickModelInteractive(ctx, sc, out, errOut)
		}
		return false, c.selectModel(ctx, strings.TrimSpace(parts[1]), sc, out, errOut)
	case "/peers":
		return false, c.printPeers(ctx, out)
	case "/use":
		if len(parts) < 2 {
			return false, c.pickPeerInteractive(ctx, sc, out, errOut)
		}
		return false, c.selectPeerByArg(ctx, parts[1], sc, out, errOut, false)
	case "/routing":
		if len(parts) < 2 {
			_, _ = io.WriteString(errOut, "usage: /routing <auto|manual|prefer_known|prefer_favorites>\n")
			return false, nil
		}
		return false, c.setRouting(ctx, parts[1], out)
	default:
		_, _ = fmt.Fprintf(errOut, "unknown command: %s (use /help)\n", parts[0])
		return false, nil
	}
}

func (c *ChatRunner) printHelp(out io.Writer) {
	_, _ = fmt.Fprintln(out, "commands:")
	_, _ = fmt.Fprintln(out, "  /help or /       show this help")
	_, _ = fmt.Fprintln(out, "  /status          show current model/peer/routing")
	_, _ = fmt.Fprintln(out, "  /models          list available models across peers")
	_, _ = fmt.Fprintln(out, "  /model [name|#]  select model (no arg opens picker)")
	_, _ = fmt.Fprintln(out, "  /peers           list peers (* marks self)")
	_, _ = fmt.Fprintln(out, "  /use [peer_id|#] select manual peer (no arg opens picker)")
	_, _ = fmt.Fprintln(out, "  /routing <auto|manual|prefer_known|prefer_favorites>")
	_, _ = fmt.Fprintln(out, "  /quit            exit chat")
}

func (c *ChatRunner) printSlashMenu(out io.Writer) {
	_, _ = fmt.Fprintln(out, "slash menu:")
	_, _ = fmt.Fprintln(out, "  selection: /status  /models  /model [name|#]  /peers  /use [peer_id|#]")
	_, _ = fmt.Fprintln(out, "  routing:   /routing <auto|manual|prefer_known|prefer_favorites>")
	_, _ = fmt.Fprintln(out, "  session:   /help  /quit")
}

func (c *ChatRunner) printStatus(ctx context.Context, out io.Writer) error {
	rs, err := c.fetchRoutingSettings(ctx)
	if err != nil {
		return err
	}
	peers, _ := c.fetchPeers(ctx)
	selectedPeer := "<auto>"
	if strings.EqualFold(strings.TrimSpace(rs.RoutingPolicy), "manual") && len(rs.AllowedPeers) > 0 {
		selectedPeer = c.peerLabel(rs.AllowedPeers[0], peers)
	} else if best, ok := c.bestProviderForCurrentModel(peers); ok {
		selectedPeer = c.peerLabel(best.PeerID, peers)
	}
	_, _ = fmt.Fprintf(out, "model: %s\n", c.opts.Model)
	_, _ = fmt.Fprintf(out, "routing: %s\n", rs.RoutingPolicy)
	_, _ = fmt.Fprintf(out, "selected_peer: %s\n", selectedPeer)
	return nil
}

func (c *ChatRunner) fetchRoutingSettings(ctx context.Context) (routingSettings, error) {
	u, err := url.JoinPath(c.opts.BaseURL, "/v1/mesh/routing")
	if err != nil {
		return routingSettings{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return routingSettings{}, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return routingSettings{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return routingSettings{}, fmt.Errorf("routing request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var rs routingSettings
	if err := json.NewDecoder(resp.Body).Decode(&rs); err != nil {
		return routingSettings{}, err
	}
	return rs, nil
}

type routingSettings struct {
	RoutingPolicy string   `json:"routing_policy"`
	AllowedPeers  []string `json:"allowed_peers"`
}

func (c *ChatRunner) printPeers(ctx context.Context, out io.Writer) error {
	peers, err := c.fetchPeers(ctx)
	if err != nil {
		return err
	}
	if len(peers) == 0 {
		_, _ = io.WriteString(out, "no peers\n")
		return nil
	}
	for i, p := range peers {
		marker := " "
		if p.Self {
			marker = "*"
		}
		_, _ = fmt.Fprintf(out, "%s [%d] %s %s models=%d [%s] latency=%dms queue=%d\n", marker, i+1, p.PeerID, p.NodeName, len(p.Models), joinModelNames(p.Models), p.AvgLatencyMS, p.QueueDepth)
	}
	return nil
}

func (c *ChatRunner) selectPeer(ctx context.Context, peerID string, out io.Writer) error {
	u, err := url.JoinPath(c.opts.BaseURL, "/v1/mesh/select", peerID)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("select peer failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	_, _ = fmt.Fprintf(out, "selected peer: %s\n", peerID)
	return nil
}

func (c *ChatRunner) selectPeerByArg(ctx context.Context, peerArg string, sc *bufio.Scanner, out, errOut io.Writer, useArrow bool) error {
	peerArg = strings.TrimSpace(peerArg)
	if idx, err := strconv.Atoi(peerArg); err == nil {
		peers, pErr := c.fetchPeers(ctx)
		if pErr != nil {
			return pErr
		}
		if idx < 1 || idx > len(peers) {
			return fmt.Errorf("peer index out of range: %d", idx)
		}
		return c.selectPeerAndModel(ctx, peers[idx-1], sc, out, errOut, useArrow)
	}
	peers, err := c.fetchPeers(ctx)
	if err == nil {
		for _, p := range peers {
			if p.PeerID == peerArg {
				return c.selectPeerAndModel(ctx, p, sc, out, errOut, useArrow)
			}
		}
	}
	return c.selectPeer(ctx, peerArg, out)
}

func (c *ChatRunner) pickPeerInteractive(ctx context.Context, sc *bufio.Scanner, out, errOut io.Writer) error {
	peers, err := c.fetchPeers(ctx)
	if err != nil {
		return err
	}
	if len(peers) == 0 {
		_, _ = io.WriteString(out, "no peers\n")
		return nil
	}
	_ = c.printPeers(ctx, out)
	_, _ = io.WriteString(out, "select peer number: ")
	if !sc.Scan() {
		return nil
	}
	choice := strings.TrimSpace(sc.Text())
	if choice == "" {
		_, _ = io.WriteString(out, "peer selection cancelled\n")
		return nil
	}
	if err := c.selectPeerByArg(ctx, choice, sc, out, errOut, false); err != nil {
		_, _ = fmt.Fprintf(errOut, "peer selection error: %v\n", err)
	}
	return nil
}

func (c *ChatRunner) pickPeerArrow(ctx context.Context, out, errOut io.Writer) error {
	peers, err := c.fetchPeers(ctx)
	if err != nil {
		return err
	}
	if len(peers) == 0 {
		_, _ = io.WriteString(out, "no peers\n")
		return nil
	}
	items := make([]string, 0, len(peers))
	for i, p := range peers {
		items = append(items, fmt.Sprintf("[%d] %s %s models=%d latency=%dms queue=%d", i+1, p.PeerID, p.NodeName, len(p.Models), p.AvgLatencyMS, p.QueueDepth))
	}
	sel := promptui.Select{
		Label: "Select peer (arrow keys + Enter)",
		Items: items,
		Size:  10,
	}
	idx, _, err := sel.Run()
	if err != nil {
		return nil
	}
	return c.selectPeerAndModel(ctx, peers[idx], nil, out, errOut, true)
}

func (c *ChatRunner) selectPeerAndModel(ctx context.Context, peerDoc model.CapabilityDocument, sc *bufio.Scanner, out, errOut io.Writer, useArrow bool) error {
	if err := c.selectPeer(ctx, peerDoc.PeerID, out); err != nil {
		return err
	}
	models := make([]string, 0, len(peerDoc.Models))
	for _, m := range peerDoc.Models {
		name := strings.TrimSpace(m.Name)
		if name != "" {
			models = append(models, name)
		}
	}
	if len(models) == 0 {
		return nil
	}
	if len(models) == 1 {
		c.opts.Model = models[0]
		_, _ = fmt.Fprintf(out, "model: %s\n", c.opts.Model)
		return nil
	}
	choice := 0
	if useArrow {
		items := make([]string, 0, len(models))
		for i, m := range models {
			items = append(items, fmt.Sprintf("[%d] %s", i+1, m))
		}
		sel := promptui.Select{
			Label: "Selected peer models (arrow keys + Enter)",
			Items: items,
			Size:  10,
		}
		idx, _, err := sel.Run()
		if err == nil {
			choice = idx
		}
	} else if sc != nil {
		_, _ = io.WriteString(out, "selected peer exposes multiple models:\n")
		for i, m := range models {
			_, _ = fmt.Fprintf(out, "  [%d] %s\n", i+1, m)
		}
		_, _ = io.WriteString(out, "select model number (default: 1): ")
		if sc.Scan() {
			raw := strings.TrimSpace(sc.Text())
			if raw != "" {
				if idx, err := strconv.Atoi(raw); err == nil && idx >= 1 && idx <= len(models) {
					choice = idx - 1
				} else {
					_, _ = io.WriteString(errOut, "invalid model selection, using first model\n")
				}
			}
		}
	}
	c.opts.Model = models[choice]
	_, _ = fmt.Fprintf(out, "model: %s\n", c.opts.Model)
	return nil
}

func (c *ChatRunner) fetchPeers(ctx context.Context) ([]model.CapabilityDocument, error) {
	u, err := url.JoinPath(c.opts.BaseURL, "/v1/mesh/peers")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("peers request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var payload struct {
		Peers []model.CapabilityDocument `json:"peers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Peers, nil
}

func (c *ChatRunner) printModels(ctx context.Context, out io.Writer) error {
	models, err := c.buildModelChoices(ctx)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		_, _ = io.WriteString(out, "no models discovered\n")
		return nil
	}
	for i, m := range models {
		best := c.bestProviderForModel(m.Providers)
		_, _ = fmt.Fprintf(out, "[%d] %s providers=%d best=%s/%s\n", i+1, m.Name, len(m.Providers), best.NodeName, shortPeerID(best.PeerID))
	}
	return nil
}

func (c *ChatRunner) pickModelInteractive(ctx context.Context, sc *bufio.Scanner, out, errOut io.Writer) error {
	if err := c.printModels(ctx, out); err != nil {
		return err
	}
	_, _ = io.WriteString(out, "select model number: ")
	if !sc.Scan() {
		return nil
	}
	choice := strings.TrimSpace(sc.Text())
	if choice == "" {
		_, _ = io.WriteString(out, "model selection cancelled\n")
		return nil
	}
	if err := c.selectModel(ctx, choice, sc, out, errOut); err != nil {
		_, _ = fmt.Fprintf(errOut, "model selection error: %v\n", err)
	}
	return nil
}

func (c *ChatRunner) pickModelArrow(ctx context.Context, out io.Writer) error {
	choices, err := c.buildModelChoices(ctx)
	if err != nil {
		return err
	}
	if len(choices) == 0 {
		_, _ = io.WriteString(out, "no models discovered\n")
		return nil
	}
	items := make([]string, 0, len(choices))
	for i, m := range choices {
		best := c.bestProviderForModel(m.Providers)
		items = append(items, fmt.Sprintf("[%d] %s providers=%d best=%s/%s", i+1, m.Name, len(m.Providers), best.NodeName, shortPeerID(best.PeerID)))
	}
	sel := promptui.Select{
		Label: "Select model (arrow keys + Enter)",
		Items: items,
		Size:  10,
	}
	idx, _, err := sel.Run()
	if err != nil {
		return nil
	}
	return c.selectModel(ctx, choices[idx].Name, nil, out, io.Discard)
}

func (c *ChatRunner) selectModel(ctx context.Context, nameOrIndex string, sc *bufio.Scanner, out, errOut io.Writer) error {
	choices, err := c.buildModelChoices(ctx)
	if err != nil {
		return err
	}
	nameOrIndex = strings.TrimSpace(nameOrIndex)
	if len(choices) == 0 {
		if _, convErr := strconv.Atoi(nameOrIndex); convErr == nil {
			return fmt.Errorf("no models discovered")
		}
		c.opts.Model = nameOrIndex
		_, _ = fmt.Fprintf(out, "model: %s (manual)\n", c.opts.Model)
		return nil
	}
	var selected *modelChoice
	if idx, err := strconv.Atoi(nameOrIndex); err == nil {
		if idx < 1 || idx > len(choices) {
			return fmt.Errorf("model index out of range: %d", idx)
		}
		selected = &choices[idx-1]
	} else {
		for i := range choices {
			if choices[i].Name == nameOrIndex {
				selected = &choices[i]
				break
			}
		}
		if selected == nil {
			c.opts.Model = nameOrIndex
			_, _ = fmt.Fprintf(out, "model: %s (manual)\n", c.opts.Model)
			return nil
		}
	}
	c.opts.Model = selected.Name
	_, _ = fmt.Fprintf(out, "model: %s\n", c.opts.Model)

	if len(selected.Providers) == 1 {
		if err := c.setRouting(ctx, "manual", io.Discard); err != nil {
			return err
		}
		return c.selectPeer(ctx, selected.Providers[0].PeerID, out)
	}
	if len(selected.Providers) > 1 {
		_, _ = io.WriteString(out, "multiple providers found:\n")
		ranked := append([]model.CapabilityDocument(nil), selected.Providers...)
		sort.Slice(ranked, func(i, j int) bool { return betterProvider(ranked[i], ranked[j]) })
		if sc == nil {
			items := make([]string, 0, len(ranked)+1)
			items = append(items, "[auto-best] choose best provider")
			for i, p := range ranked {
				items = append(items, fmt.Sprintf("[%d] %s/%s latency=%dms queue=%d reliability=%.2f", i+1, p.NodeName, shortPeerID(p.PeerID), p.AvgLatencyMS, p.QueueDepth, p.Reliability))
			}
			sel := promptui.Select{
				Label: "Select provider (arrow keys + Enter)",
				Items: items,
				Size:  10,
			}
			idx, _, selErr := sel.Run()
			if selErr != nil {
				return nil
			}
			if idx == 0 {
				best := c.bestProviderForModel(ranked)
				if err := c.setRouting(ctx, "manual", io.Discard); err != nil {
					return err
				}
				return c.selectPeer(ctx, best.PeerID, out)
			}
			if err := c.setRouting(ctx, "manual", io.Discard); err != nil {
				return err
			}
			return c.selectPeer(ctx, ranked[idx-1].PeerID, out)
		}
		for i, p := range ranked {
			_, _ = fmt.Fprintf(out, "  [%d] %s/%s latency=%dms queue=%d reliability=%.2f\n", i+1, p.NodeName, shortPeerID(p.PeerID), p.AvgLatencyMS, p.QueueDepth, p.Reliability)
		}
		_, _ = io.WriteString(out, "choose provider number or 'a' for auto-best: ")
		if !sc.Scan() {
			return nil
		}
		choice := strings.TrimSpace(sc.Text())
		if choice == "" {
			_, _ = io.WriteString(out, "keeping current routing policy\n")
			return nil
		}
		if strings.EqualFold(choice, "a") {
			best := c.bestProviderForModel(ranked)
			if err := c.setRouting(ctx, "manual", io.Discard); err != nil {
				return err
			}
			return c.selectPeer(ctx, best.PeerID, out)
		}
		idx, err := strconv.Atoi(choice)
		if err != nil || idx < 1 || idx > len(ranked) {
			_, _ = io.WriteString(errOut, "invalid provider selection\n")
			return nil
		}
		if err := c.setRouting(ctx, "manual", io.Discard); err != nil {
			return err
		}
		return c.selectPeer(ctx, ranked[idx-1].PeerID, out)
	}
	return nil
}

func (c *ChatRunner) EnsureModelAvailable(ctx context.Context, out io.Writer, interactive bool) error {
	_ = c.setRouting(ctx, "auto", io.Discard)
	choices, err := c.buildModelChoices(ctx)
	if err != nil {
		return nil
	}
	if len(choices) == 0 {
		return nil
	}
	var selected *modelChoice
	for _, m := range choices {
		if m.Name == c.opts.Model {
			mc := m
			selected = &mc
			break
		}
	}
	if selected == nil {
		sort.Slice(choices, func(i, j int) bool {
			a := c.bestProviderForModel(choices[i].Providers)
			b := c.bestProviderForModel(choices[j].Providers)
			if betterProvider(a, b) {
				return true
			}
			if betterProvider(b, a) {
				return false
			}
			return choices[i].Name < choices[j].Name
		})
		mc := choices[0]
		selected = &mc
		c.opts.Model = selected.Name
		best := c.bestProviderForModel(selected.Providers)
		if best.PeerID != "" {
			_, _ = fmt.Fprintf(out, "auto-selected model: %s (best peer: %s)\n", c.opts.Model, c.peerLabel(best.PeerID, selected.Providers))
		} else {
			_, _ = fmt.Fprintf(out, "auto-selected model: %s\n", c.opts.Model)
		}
	}
	if selected != nil && len(selected.Providers) > 1 {
		if interactive {
			items := make([]string, 0, len(selected.Providers)+1)
			items = append(items, fmt.Sprintf("[auto] best peer for %s", selected.Name))
			ranked := append([]model.CapabilityDocument(nil), selected.Providers...)
			sort.Slice(ranked, func(i, j int) bool { return betterProvider(ranked[i], ranked[j]) })
			for i, p := range ranked {
				items = append(items, fmt.Sprintf("[%d] %s (%s) models=[%s] latency=%dms queue=%d", i+1, p.NodeName, compactPeerID(p.PeerID), joinModelNames(p.Models), p.AvgLatencyMS, p.QueueDepth))
			}
			sel := promptui.Select{
				Label: "Multiple peers available for selected model",
				Items: items,
				Size:  10,
			}
			idx, _, err := sel.Run()
			if err == nil && idx > 0 {
				chosen := ranked[idx-1]
				_ = c.setRouting(ctx, "manual", io.Discard)
				_ = c.selectPeer(ctx, chosen.PeerID, out)
			}
		} else {
			_, _ = fmt.Fprintf(out, "multiple peers available for %s; use /use to pin one peer or keep routing auto\n", selected.Name)
		}
	}
	return nil
}

func (c *ChatRunner) buildModelChoices(ctx context.Context) ([]modelChoice, error) {
	peers, err := c.fetchPeers(ctx)
	if err != nil {
		return nil, err
	}
	byModel := map[string][]model.CapabilityDocument{}
	for _, p := range peers {
		if p.Self {
			continue
		}
		for _, m := range p.Models {
			name := strings.TrimSpace(m.Name)
			if name == "" {
				continue
			}
			byModel[name] = append(byModel[name], p)
		}
	}
	out := make([]modelChoice, 0, len(byModel))
	for name, providers := range byModel {
		out = append(out, modelChoice{Name: name, Providers: providers})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (c *ChatRunner) bestProviderForModel(providers []model.CapabilityDocument) model.CapabilityDocument {
	if len(providers) == 0 {
		return model.CapabilityDocument{}
	}
	best := providers[0]
	for i := 1; i < len(providers); i++ {
		if betterProvider(providers[i], best) {
			best = providers[i]
		}
	}
	return best
}

func betterProvider(a, b model.CapabilityDocument) bool {
	if a.Reliability != b.Reliability {
		return a.Reliability > b.Reliability
	}
	if a.QueueDepth != b.QueueDepth {
		return a.QueueDepth < b.QueueDepth
	}
	if a.AvgLatencyMS != b.AvgLatencyMS {
		return a.AvgLatencyMS < b.AvgLatencyMS
	}
	return a.PeerID < b.PeerID
}

func shortPeerID(peerID string) string {
	if len(peerID) <= 12 {
		return peerID
	}
	return peerID[:12]
}

func compactPeerID(peerID string) string {
	if len(peerID) <= 7 {
		return peerID
	}
	return peerID[:3] + "..." + peerID[len(peerID)-3:]
}

func (c *ChatRunner) peerLabel(peerID string, peers []model.CapabilityDocument) string {
	for _, p := range peers {
		if p.PeerID == peerID {
			name := strings.TrimSpace(p.NodeName)
			if name == "" {
				return compactPeerID(peerID)
			}
			return fmt.Sprintf("%s (%s)", name, compactPeerID(peerID))
		}
	}
	return compactPeerID(peerID)
}

func (c *ChatRunner) bestProviderForCurrentModel(peers []model.CapabilityDocument) (model.CapabilityDocument, bool) {
	providers := make([]model.CapabilityDocument, 0, len(peers))
	for _, p := range peers {
		if p.Self {
			continue
		}
		if p.SupportsModel(c.opts.Model) {
			providers = append(providers, p)
		}
	}
	if len(providers) == 0 {
		return model.CapabilityDocument{}, false
	}
	return c.bestProviderForModel(providers), true
}

func joinModelNames(models []model.ModelSpec) string {
	if len(models) == 0 {
		return "-"
	}
	names := make([]string, 0, len(models))
	for _, m := range models {
		n := strings.TrimSpace(m.Name)
		if n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return "-"
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func (c *ChatRunner) setRouting(ctx context.Context, policy string, out io.Writer) error {
	body, _ := json.Marshal(map[string]any{"routing_policy": policy})
	u, err := url.JoinPath(c.opts.BaseURL, "/v1/mesh/routing")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("routing update failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	_, _ = fmt.Fprintf(out, "routing policy: %s\n", policy)
	return nil
}
