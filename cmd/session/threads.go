package session

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/spf13/cobra"
)

var sessionThreadStore = conversation.DefaultThreadStore

type threadContext struct {
	Links   []conversation.Link `json:"thread_links"`
	Related []sessionView       `json:"related_conversations,omitempty"`
	Warning string              `json:"thread_warning,omitempty"`
}

func readThreadContext(views []sessionView, all []transcript.SessionMeta, liveSeeds ...conversation.Identity) threadContext {
	result := threadContext{Links: []conversation.Link{}}
	store, err := sessionThreadStore()
	if err != nil {
		result.Warning = err.Error()
		return result
	}
	links, err := store.Links()
	if err != nil {
		result.Warning = err.Error()
		return result
	}
	seeds := append([]conversation.Identity(nil), liveSeeds...)
	for _, view := range views {
		seeds = append(seeds, conversation.Identity{Harness: view.Harness, SessionID: view.ID})
	}
	result.Links = conversation.Threads(links, seeds)
	wanted := map[conversation.Identity]bool{}
	for _, link := range result.Links {
		wanted[link.Source] = true
		if link.Target != nil {
			wanted[*link.Target] = true
		}
	}
	for _, view := range views {
		delete(wanted, conversation.Identity{Harness: view.Harness, SessionID: view.ID})
	}
	for _, meta := range all {
		if wanted[conversation.Identity{Harness: meta.Harness, SessionID: meta.ID}] {
			result.Related = append(result.Related, sessionViews([]transcript.SessionMeta{meta})[0])
		}
	}
	return result
}

var sessionThreadsCmd = &cobra.Command{
	Use:   "threads",
	Short: "Read recorded conversation continuations, including closed sessions",
	Args:  cobra.NoArgs,
	RunE:  func(cmd *cobra.Command, _ []string) error { return writeThreads(cmd, nil) },
}

var sessionThreadCmd = &cobra.Command{
	Use:               "thread <harness>:<session-id> | <session-id>",
	Short:             "Follow a conversation's handoffs in both directions",
	Args:              cobra.MaximumNArgs(2),
	ValidArgsFunction: completeConversation,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		selector, err := conversationIdentityFromArgs(cmd, jsonOutput, "thread", args)
		if err != nil {
			return err
		}
		// Exact qualified identities still work when the transcript is gone.
		if selector.Harness != "" {
			store, err := sessionThreadStore()
			if err != nil {
				return cli.CommandFailure(cmd, jsonOutput, "conversation_history_unavailable", err)
			}
			links, err := store.Links()
			if err != nil {
				return cli.CommandFailure(cmd, jsonOutput, "conversation_history_unavailable", err)
			}
			for _, link := range links {
				if link.Source == selector || link.Target != nil && *link.Target == selector {
					return writeThreads(cmd, &selector)
				}
			}
		}
		cfg, err := sessionCommandsLoadConfig()
		if err != nil {
			return cli.ConfigFailure(cmd, jsonOutput, err)
		}
		meta, err := resolveConversationOrFail(cmd, jsonOutput, cfg, sessionCommandsSessionReaders(), selector)
		if err != nil {
			return err
		}
		identity, _ := conversation.New(meta.Harness, meta.ID)
		return writeThreads(cmd, &identity)
	},
}

func writeThreads(cmd *cobra.Command, identity *conversation.Identity) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	store, err := sessionThreadStore()
	if err != nil {
		return cli.CommandFailure(cmd, jsonOutput, "conversation_history_unavailable", err)
	}
	links, err := store.Links()
	if err != nil {
		return cli.CommandFailure(cmd, jsonOutput, "conversation_history_unavailable", err)
	}
	if identity != nil {
		links = conversation.Thread(links, *identity)
	}
	if jsonOutput {
		return cli.WriteJSON(cmd, threadContext{Links: links})
	}
	if len(links) == 0 {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No recorded conversation continuations.")
		return err
	}
	for _, link := range links {
		target := "awaiting conversation (" + link.TargetKey + ")"
		if link.Target != nil {
			target = link.Target.Harness + ":" + link.Target.SessionID
		}
		if _, err = fmt.Fprintf(cmd.OutOrStdout(), "%s:%s → %s · %s\n", link.Source.Harness, link.Source.SessionID, target, link.Kind); err != nil {
			return err
		}
	}
	return nil
}

func init() {
	cli.AddJSONFlag(sessionThreadsCmd, nil)
	cli.AddJSONFlag(sessionThreadCmd, nil)
	sessionCmd.AddCommand(sessionThreadsCmd, sessionThreadCmd)
}
