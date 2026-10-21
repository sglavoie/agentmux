package debug

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/spf13/cobra"
)

// subscribeSchemaVersion versions the `debug subscribe --json` line records.
// Like `debug tail --follow --json`, the command writes one record per line:
// the ack first, then one per frame.
const subscribeSchemaVersion = 1

// subscribeClientName is how the command identifies itself in daemon health.
const subscribeClientName = "amx debug subscribe"

// debugSubscription is the stream seam the command reads through.
type debugSubscription interface {
	Next() (protocol.StreamFrame, error)
	Close() error
}

var debugSubscribe = func(ctx context.Context, req protocol.SubscribeRequest) (protocol.SubscriptionAck, debugSubscription, error) {
	sub, err := ipc.NewClient().Subscribe(ctx, req)
	if err != nil {
		return protocol.SubscriptionAck{}, nil, err
	}
	return sub.Ack, sub, nil
}

var debugSubscribeFrames int
var debugSubscribeJSON bool

var debugSubscribeCmd = &cobra.Command{
	Use:   "subscribe",
	Short: "Open a companion stream and print the frames the daemon publishes",
	Long: `Subscribe to the daemon exactly as a companion app does and print each frame:
snapshots as a one-line session summary, heartbeats, and the bye that ends
the stream. --frames N stops after N frames; without it the command runs until
the stream ends or is interrupted. It takes no selector.`,
	Example:      "  amx debug subscribe --frames 3\n  amx debug subscribe --json",
	Annotations:  map[string]string{debugNoSelectorsAnnotation: "true"},
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runDebugSubscribe,
}

func init() {
	debugSubscribeCmd.Flags().IntVar(&debugSubscribeFrames, "frames", 0, "stop after N frames (0 means until the stream ends)")
	cli.AddReportJSONFlag(debugSubscribeCmd, &debugSubscribeJSON, "one record per line")
	debugCmd.AddCommand(debugSubscribeCmd)
}

// subscribeRecord is one `--json` line: exactly one of Ack and Frame is set.
type subscribeRecord struct {
	SchemaVersion int                       `json:"schema_version"`
	Ack           *protocol.SubscriptionAck `json:"ack,omitempty"`
	Frame         *protocol.StreamFrame     `json:"frame,omitempty"`
}

func runDebugSubscribe(cmd *cobra.Command, _ []string) error {
	if debugSubscribeFrames < 0 {
		return fmt.Errorf("--frames must be zero or positive")
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	build := buildinfo.Current()
	ack, sub, err := debugSubscribe(ctx, protocol.SubscribeRequest{
		Protocol:      protocol.CompanionProtocolVersion,
		Client:        subscribeClientName,
		ClientVersion: build.Version,
		AMXBuild:      &build,
	})
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer func() { _ = sub.Close() }()

	out := cmd.OutOrStdout()
	if debugSubscribeJSON {
		if err := writeJSONLine(out, subscribeRecord{SchemaVersion: subscribeSchemaVersion, Ack: &ack}); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintf(out, "subscribed: %s protocol=%d server_max=%d heartbeat=%dms features=%s\n",
		ack.SubscriberID, ack.Protocol, ack.ServerProtocolMax, ack.HeartbeatMS, strings.Join(ack.Features, ",")); err != nil {
		return err
	}

	for written := 0; debugSubscribeFrames == 0 || written < debugSubscribeFrames; written++ {
		frame, err := sub.Next()
		if err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("reading stream: %w", err)
		}
		if debugSubscribeJSON {
			err = writeJSONLine(out, subscribeRecord{SchemaVersion: subscribeSchemaVersion, Frame: &frame})
		} else {
			err = writeSubscribeFrame(out, frame)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// writeSubscribeFrame prints one frame as a single human-readable line.
func writeSubscribeFrame(w io.Writer, frame protocol.StreamFrame) error {
	switch frame.Type {
	case protocol.FrameSnapshot:
		var snap protocol.CompanionSnapshot
		if err := json.Unmarshal(frame.Data, &snap); err != nil {
			_, werr := fmt.Fprintf(w, "#%d snapshot (undecodable: %v)\n", frame.Seq, err)
			return werr
		}
		// The digest makes a republish visible even when only a field the
		// summary omits (a last-event timestamp, say) moved.
		sum := sha256.Sum256(frame.Data)
		_, err := fmt.Fprintf(w, "#%d snapshot digest=%s %s\n", frame.Seq, hex.EncodeToString(sum[:4]), summarizeSnapshot(snap))
		return err
	case protocol.FrameBye:
		var bye protocol.Bye
		_ = json.Unmarshal(frame.Data, &bye)
		_, err := fmt.Fprintf(w, "#%d bye reason=%s\n", frame.Seq, emptyDash(bye.Reason))
		return err
	default:
		_, err := fmt.Fprintf(w, "#%d %s\n", frame.Seq, frame.Type)
		return err
	}
}

// summarizeSnapshot renders the facts a reader watches change: readiness, the
// attention counts, and each session's window and icon, with * marking one
// that needs attention.
func summarizeSnapshot(snap protocol.CompanionSnapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ready=%t kitty_ready=%t sessions=%d attention=%d", snap.Daemon.Ready, snap.Kitty.Ready, len(snap.Sessions), snap.Attention.Total)
	if snap.Kitty.Stale {
		b.WriteString(" kitty_stale")
	}
	if snap.Truncated {
		b.WriteString(" truncated")
	}
	if len(snap.ASL.Waiting) > 0 {
		fmt.Fprintf(&b, " asl_waiting=%d", len(snap.ASL.Waiting))
	}
	if len(snap.Sessions) > 0 {
		b.WriteString(" [")
		for i, s := range snap.Sessions {
			if i > 0 {
				b.WriteByte(' ')
			}
			fmt.Fprintf(&b, "%d:%s", s.WindowID, emptyDash(s.IconState))
			if s.Attention {
				b.WriteByte('*')
			}
		}
		b.WriteByte(']')
	}
	return b.String()
}
