package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	openai "github.com/sashabaranov/go-openai"
)

func main() {
	p := tea.NewProgram(initialModel())

	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}

type deltaMsg string

func waitForDelta(msg chan string) tea.Cmd {
	return func() tea.Msg {
		return deltaMsg(<-msg)
	}
}

type (
	errMsg error
)

type model struct {
	goos  string
	shell string

	client *openai.Client

	width  int
	height int

	viewport viewport.Model
	textarea textarea.Model
	err      error

	inputMessage chan string
	deltaMessage chan string
	messages     []string
}

func initialModel() model {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "sh"
	} else {
		shell = path.Base(shell)
	}

	client := openai.NewClient(os.Getenv("OPENAI_API_KEY"))

	ta := textarea.New()
	ta.Placeholder = "Type here"
	ta.Focus()

	ta.Prompt = "┃ "
	ta.CharLimit = 280

	ta.SetWidth(300)
	ta.SetHeight(3)

	// Remove cursor line styling
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()

	ta.ShowLineNumbers = false

	ta.KeyMap.InsertNewline.SetEnabled(false)

	vp := viewport.New(100, 5)
	vp.SetContent(`Welcome to the chat room!
Type a message and press Enter to send.`)

	return model{
		goos:  runtime.GOOS,
		shell: shell,

		client: client,

		textarea: ta,
		viewport: vp,
		err:      nil,

		inputMessage: make(chan string),
		deltaMessage: make(chan string),
		messages:     []string{},
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.createChatCompletion(),
		waitForDelta(m.deltaMessage),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmds []tea.Cmd

		tiCmd tea.Cmd
		vpCmd tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			fmt.Println(m.textarea.Value())
			return m, tea.Quit
		case tea.KeyEnter:
			message := m.textarea.Value()
			m.messages = append(m.messages, lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Render("You: ")+message)
			m.messages = append(m.messages, lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Render("System: "))
			m.viewport.SetContent(strings.Join(m.messages, "\n"))
			m.textarea.Reset()
			m.viewport.GotoBottom()

			m.inputMessage <- message
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		m.textarea.SetWidth(m.width)

		// TODO: Sync viewport width
	case deltaMsg:
		m.messages[len(m.messages)-1] += string(msg)
		m.viewport.SetContent(strings.Join(m.messages, "\n"))
		m.viewport.GotoBottom()
		cmds = append(cmds, waitForDelta(m.deltaMessage))
	case errMsg:
		m.err = msg
		return m, nil
	}

	m.textarea, tiCmd = m.textarea.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, tiCmd, vpCmd)

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	return fmt.Sprintf(
		"%s\n\n%s",
		m.viewport.View(),
		m.textarea.View(),
	) + "\n\n"
}

func (m model) createChatCompletion() tea.Cmd {
	return func() tea.Msg {
		for {
			ctx := context.Background()
			req := openai.ChatCompletionRequest{
				Model: openai.GPT3Dot5Turbo,
				Messages: []openai.ChatCompletionMessage{
					{
						Role:    openai.ChatMessageRoleUser,
						Content: <-m.inputMessage,
					},
				},
			}
			stream, err := m.client.CreateChatCompletionStream(ctx, req)
			if err != nil {
				return errMsg(err)
			}
			defer stream.Close()

			for {
				response, err := stream.Recv()
				if errors.Is(err, io.EOF) {
					break
				}

				if err != nil {
					return errMsg(err)
				}

				m.deltaMessage <- response.Choices[0].Delta.Content
			}
		}
	}
}
