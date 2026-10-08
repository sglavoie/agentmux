// Package ipc is how a request reaches the daemon and how an answer gets
// back.
//
// The server side is the Unix-socket server with its owner-only 0600 bind and
// drain-on-close lifecycle, the newline-delimited JSON framing with its
// request and response size ceilings and bad-payload throttling, the bounded
// single-consumer ingress queue that serializes application and degrades
// inline under saturation, the request-ID dedupe cache that makes a retried
// delivery at-most-once, and the transport's own request-outcome and
// queue-latency counters. The client side is all of it: the socket path, the
// send deadlines, daemon auto-start and restart, the typed health,
// event-export, and ASL calls, and the subscribe client
// (subscription_client.go), which reads frames until a bye, EOF, or
// cancellation of its context.
//
// A connection carries exactly one request/response pair and there is no
// multiplexing, so a second answer means a second connection — except
// subscribe, whose ack opens a stream of server-to-client frames on the same
// connection (subscription.go). That stream is one-directional, bounded, and
// coalesced, and never carries a second request: client lines after the
// subscribe are read only to see EOF. A StreamSink keeps the latest snapshot
// in a single slot that the writer drains before a 64-frame queue, so a newer
// snapshot replaces an unwritten one; a full queue or a frame write exceeding
// its 2s deadline ends the subscriber as a slow consumer, with a best-effort
// bye; a frame over protocol.CompanionFrameMaxBytes is dropped and counted;
// and a ninth concurrent subscriber is refused with subscriber_limit. Close
// ends every live stream with bye{shutdown} before it waits on connections,
// because a stream never ends on its own and would otherwise hold the drain
// forever. Streams are offered through the optional SubscriptionHandler
// interface; a handler that does not implement it answers subscribe as an
// unknown request type.
//
// The package sees its application only as the three-method RequestHandler
// seam, which is what keeps it from ever importing the daemon root. It
// dispatches a request to the application and routes it onto the serialized
// or the inline path; naming the concrete behavior a request type means — and
// every reducer, renderer, and notifier mutation under stateMu — stays in the
// root, which implements RequestHandler.
package ipc
