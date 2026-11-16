package mcp

import (
	"context"
	"github.com/base698/amythest/internal/work"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerWorkTools(s *sdk.Server, d Deps) {
	addTool(s, d, &sdk.Tool{Name: "work_card", Description: "Read bounded agent runs, artifacts, reduced chat and current slots for a card. Does not acknowledge review or read full transcripts."}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Card string `json:"card_id"`
	}) (*sdk.CallToolResult, work.CardData, error) { out, e := d.Work.Snapshot(in.Card, ""); return nil, out, e })
	addTool(s, d, &sdk.Tool{Name: "work_inbox", Description: "Read current needs-review, blocked, stalled and offline attention items."}, func(ctx context.Context, req *sdk.CallToolRequest, in struct{}) (*sdk.CallToolResult, struct {
		Items []work.Attention `json:"items"`
	}, error) { out, e := d.Work.Inbox(); return nil, struct {
		Items []work.Attention `json:"items"`
	}{out}, e })
	addTool(s, d, &sdk.Tool{Name: "work_search", Description: "Search retained Work text with card, project, kind and inclusive date filters. Returns at most 100 results. Full transcripts are excluded."}, func(ctx context.Context, req *sdk.CallToolRequest, in work.SearchOptions) (*sdk.CallToolResult, struct {
		Results []work.SearchHit `json:"results"`
	}, error) { out, e := d.Work.Search(in); return nil, struct {
		Results []work.SearchHit `json:"results"`
	}{out}, e })
}
