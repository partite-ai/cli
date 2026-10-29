package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/partite-ai/partite-cli/internal/cmd/accesspoint"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convapi"
	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convui"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	"github.com/spf13/cobra"
)

func NewChat(app *cmdutil.App) *cobra.Command {
	var rf requestFlags
	var thinking bool
	cmd := &cobra.Command{
		Use:     "chat <mesh>",
		GroupID: "runtime",
		Short:   "Chat with a mesh at the terminal",
		Long: `Hold a conversation with a mesh: type a message, see the agents work and
answer, and type the next one. All messages go to one conversation.

Commands at the prompt:
  /new   start a new conversation
  /id    show the conversation id
  /exit  leave (or press Ctrl-D)

A new conversation needs the mesh version: --label, --version, --route or
--draft. chat creates the conversation when it starts, to read its intents.

Ctrl-C while the mesh works cancels the request; at the prompt it leaves.
chat needs a terminal. Scripts and agents use partite run.

` + accesspoint.CallerHelp,
		Example: `  partite chat support --label prod
  partite chat support --draft --thinking
  partite chat support --conversation rcnv_8Xk2
  partite chat support --label prod --access-point partners --subject alice --claim tier=gold`,
		Args: cobra.ExactArgs(1),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return rf.caller.Apply(cmd, app)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !app.Interactive() {
				return &cmdutil.Error{Code: cmdutil.ExitUsage, Msg: "chat needs a terminal", Hint: "Send single requests with: partite run " + args[0] + " TEXT"}
			}
			// chat handles Ctrl-C itself, so that one does not end the session.
			base := context.WithoutCancel(cmd.Context())
			s, err := openSession(base, app, cmd, args[0], &rf)
			if err != nil {
				return err
			}
			if s.conversationID == "" {
				if err := s.create(base, convapi.NewConversation{}); err != nil {
					return err
				}
			}
			intent, err := chatIntent(s, rf.intent)
			if err != nil {
				return err
			}

			var mu sync.Mutex
			var cancelTurn context.CancelFunc
			sigs := make(chan os.Signal, 1)
			signal.Notify(sigs, os.Interrupt)
			defer signal.Stop(sigs)
			go func() {
				for range sigs {
					mu.Lock()
					if cancelTurn != nil {
						cancelTurn()
					}
					mu.Unlock()
				}
			}()
			interruptible := func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(base)
				mu.Lock()
				cancelTurn = cancel
				mu.Unlock()
				return ctx, cancel
			}

			style := convui.NewStyle(app.Err)
			fmt.Fprintln(app.Err, style.Dim(fmt.Sprintf("Chatting with %s (intent %s). Type /exit or press Ctrl-D to leave.", args[0], intent.Name)))
			lr := convui.NewLineReader(app.In)
			prop, _ := convui.TextProperty(intent.Schema)
			for {
				ctx, cancel := interruptible()
				fmt.Fprint(app.Err, "\n"+style.Bold(style.Green("› ")))
				line, err := lr.ReadLine(ctx)
				cancel()
				if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
					fmt.Fprintln(app.Err)
					break
				}
				if err != nil {
					return err
				}
				line = strings.TrimSpace(line)
				switch line {
				case "":
					continue
				case "/exit", "/quit":
					return chatGoodbye(app, style, s)
				case "/new":
					if s.target == (convapi.Target{}) {
						fmt.Fprintln(app.Err, "To start new conversations, start chat without --conversation.")
						continue
					}
					s.conversationID = ""
					fmt.Fprintln(app.Err, style.Dim("The next message starts a new conversation."))
					continue
				case "/id":
					if s.conversationID == "" {
						fmt.Fprintln(app.Err, "No conversation yet. Send a message to start one.")
					} else {
						fmt.Fprintln(app.Err, s.conversationID)
					}
					continue
				}
				ctx, cancel = interruptible()
				err = chatTurn(ctx, app, s, lr, intent.Name, prop, line, thinking)
				cancel()
				if err != nil && !errors.Is(err, context.Canceled) {
					app.PrintError(app.Err, app.Classify(err))
				}
			}
			return chatGoodbye(app, style, s)
		},
	}
	rf.register(cmd)
	cmd.Flags().BoolVar(&thinking, "thinking", false, "show the model's thinking, dimmed")
	return cmd
}

// chatIntent picks the intent to chat with, which must take text.
func chatIntent(s *session, name string) (convapi.Intent, error) {
	intent, err := convui.PickIntent(s.intents, name, true, s.intentsCmd())
	if err != nil {
		return intent, err
	}
	if _, ok := convui.TextProperty(intent.Schema); !ok {
		return intent, &cmdutil.Error{
			Code: cmdutil.ExitUsage,
			Msg:  fmt.Sprintf("intent %s takes structured input, so chat cannot fill it with text", intent.Name),
			Hint: "Send it with: partite run " + s.meshRef + " --intent " + intent.Name + " --input JSON",
		}
	}
	return intent, nil
}

func chatTurn(ctx context.Context, app *cmdutil.App, s *session, lr *convui.LineReader, intent, prop, text string, thinking bool) error {
	msg, err := json.Marshal(map[string]string{prop: text})
	if err != nil {
		return err
	}
	start := func(ctx context.Context, h convapi.Handler) (convapi.Result, error) {
		if s.conversationID != "" {
			return s.client.StartRequest(ctx, s.conversationID, convapi.NewRequest{Intent: intent, Input: msg}, nil, h)
		}
		return s.client.StartConversation(ctx, convapi.NewConversation{Target: s.target, Intent: intent, Input: msg}, nil, h)
	}
	res, err := convui.Start(ctx, app, s.client, lr, convui.FollowOptions{
		Thinking:          thinking,
		Answer:            true,
		CancelOnInterrupt: true,
	}, start)
	if res.ConversationID != "" {
		s.conversationID = res.ConversationID
	}
	return err
}

func chatGoodbye(app *cmdutil.App, style convui.Style, s *session) error {
	if s.conversationID != "" {
		fmt.Fprintln(app.Err, style.Dim(fmt.Sprintf("Conversation %s. Continue it with: partite chat %s --conversation %s", s.conversationID, s.meshRef, s.conversationID)))
	}
	return nil
}
