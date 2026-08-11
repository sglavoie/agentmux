package debugger

import (
	"sort"
	"strconv"
	"strings"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func buildEvidenceGraph(
	records []diagnostics.TraceEnvelope,
	hooks []HookProfile,
	maxNodes int,
	maxEdges int,
	truncation *ExplanationTruncation,
) (EvidenceGraph, []string) {
	graph := EvidenceGraph{Nodes: make([]EvidenceNode, 0, len(records)+len(hooks)), Edges: make([]EvidenceEdge, 0)}
	nodeIDs := make([]string, len(records))
	used := make(map[string]int)
	for i := range records {
		record := records[i]
		base := recordNodeID(record)
		used[base]++
		id := base
		if used[base] > 1 {
			id = base + ":" + strconv.Itoa(used[base])
		}
		nodeIDs[i] = id
		copyRecord := record
		graph.Nodes = append(graph.Nodes, EvidenceNode{ID: id, Kind: "trace", Identity: evidenceIdentity(record), Record: &copyRecord})
	}
	for i := range hooks {
		if len(graph.Nodes) >= maxNodes {
			truncation.PrunedNodeCandidates++
			addExplanationTruncationReason(truncation, TruncationMaxNodes)
			continue
		}
		profile := hooks[i]
		id := "hook:" + profile.Profile + ":" + profile.Harness
		copyProfile := profile
		graph.Nodes = append(graph.Nodes, EvidenceNode{ID: id, Kind: "hook_status", Hook: &copyProfile})
	}
	edgeCandidates := graphEdges(graph.Nodes)
	if len(edgeCandidates) > maxEdges {
		truncation.PrunedEdgeCandidates = len(edgeCandidates) - maxEdges
		addExplanationTruncationReason(truncation, TruncationMaxEdges)
		edgeCandidates = edgeCandidates[:maxEdges]
	}
	graph.Edges = edgeCandidates
	return graph, nodeIDs
}

func recordNodeID(record diagnostics.TraceEnvelope) string {
	if record.SourceID != "" && record.SourceSequence != 0 {
		return "trace:" + record.SourceID + ":" + strconv.FormatUint(record.SourceSequence, 10)
	}
	if record.SpanID != "" {
		return "span:" + record.SpanID
	}
	return "record:" + record.TraceID + ":" + record.Stage
}

func evidenceIdentity(record diagnostics.TraceEnvelope) EvidenceIdentity {
	normalizedSource := ""
	if kind, key := record.Evidence["source_kind"], record.Evidence["source_key"]; kind != "" || key != "" {
		normalizedSource = kind + ":" + key
	}
	return EvidenceIdentity{
		TraceID: record.TraceID, ParentTraceID: record.ParentTraceID, RequestID: record.RequestID,
		SourceEventIDs: sourceEventIDs(record), ProducerSourceID: record.SourceID,
		ProducerSourceSequence: record.SourceSequence, NormalizedSourceID: normalizedSource,
		NormalizedSourceSequence: parseUint(record.Evidence["source_sequence"]),
		DaemonSequence:           parseUint(record.Evidence["daemon_sequence"]),
		RenderGeneration:         parseUint(firstNonEmptyString(record.Evidence["generation"], record.Evidence["render_generation"])),
		LaunchID:                 record.LaunchID, WindowID: record.WindowID, SessionID: record.SessionID, ASLRunID: record.RunID,
		TurnID: record.Evidence["turn_id"], ToolUseID: record.Evidence["tool_use_id"],
	}
}

func graphEdges(nodes []EvidenceNode) []EvidenceEdge {
	groups := make(map[string][]string)
	values := make(map[string]string)
	for _, node := range nodes {
		id := node.Identity
		groupJoin(groups, values, "trace_id", id.TraceID, node.ID)
		groupJoin(groups, values, "request_id", id.RequestID, node.ID)
		for _, sourceEventID := range id.SourceEventIDs {
			groupJoin(groups, values, "source_event_id", sourceEventID, node.ID)
		}
		groupJoin(groups, values, "producer_source_id", id.ProducerSourceID, node.ID)
		groupJoin(groups, values, "normalized_source_id", id.NormalizedSourceID, node.ID)
		if id.DaemonSequence > 0 {
			groupJoin(groups, values, "daemon_sequence", strconv.FormatUint(id.DaemonSequence, 10), node.ID)
		}
		if id.RenderGeneration > 0 {
			groupJoin(groups, values, "render_generation", strconv.Itoa(id.WindowID)+":"+strconv.FormatUint(id.RenderGeneration, 10), node.ID)
		}
		groupJoin(groups, values, "launch_id", id.LaunchID, node.ID)
		if id.WindowID > 0 && id.SessionID != "" {
			groupJoin(groups, values, "window_session", strconv.Itoa(id.WindowID)+":"+id.SessionID, node.ID)
		}
		groupJoin(groups, values, "session_id", id.SessionID, node.ID)
		groupJoin(groups, values, "asl_run_id", id.ASLRunID, node.ID)
		if node.Record != nil {
			groupJoin(groups, values, "turn_id", scopedRecordID(*node.Record, id.TurnID), node.ID)
			groupJoin(groups, values, "tool_use_id", scopedRecordID(*node.Record, id.ToolUseID), node.ID)
		}
		if node.Hook != nil {
			groupJoin(groups, values, "hook_scope", node.Hook.Profile+":"+node.Hook.Harness, node.ID)
		}
		if node.Record != nil && node.Record.Profile != "" && node.Record.Harness != "" {
			groupJoin(groups, values, "hook_scope", node.Record.Profile+":"+node.Record.Harness, node.ID)
		}
	}
	edges := make([]EvidenceEdge, 0)
	seen := make(map[string]struct{})
	for key, ids := range groups {
		if len(ids) < 2 {
			continue
		}
		parts := strings.SplitN(key, "\x00", 2)
		for _, id := range ids[1:] {
			addGraphEdge(&edges, seen, EvidenceEdge{From: ids[0], To: id, Relation: parts[0], Value: values[key]})
		}
	}
	for _, child := range nodes {
		if child.Identity.ParentTraceID == "" {
			continue
		}
		for _, parent := range nodes {
			if parent.Identity.TraceID == child.Identity.ParentTraceID {
				addGraphEdge(&edges, seen, EvidenceEdge{From: parent.ID, To: child.ID, Relation: "parent_trace_id", Value: child.Identity.ParentTraceID})
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		if edges[i].Relation != edges[j].Relation {
			return edges[i].Relation < edges[j].Relation
		}
		return edges[i].Value < edges[j].Value
	})
	return edges
}

func groupJoin(groups map[string][]string, values map[string]string, relation, value, nodeID string) {
	if value == "" {
		return
	}
	key := relation + "\x00" + value
	groups[key] = append(groups[key], nodeID)
	values[key] = value
}

func addGraphEdge(edges *[]EvidenceEdge, seen map[string]struct{}, edge EvidenceEdge) {
	key := edge.From + "\x00" + edge.To + "\x00" + edge.Relation + "\x00" + edge.Value
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*edges = append(*edges, edge)
}
