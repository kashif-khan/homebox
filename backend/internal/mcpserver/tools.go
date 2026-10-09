package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog/log"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/data/types"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
)

func itoa(n int) string { return strconv.Itoa(n) }

// call carries everything a tool handler needs. Handlers receive it instead of
// reaching for globals, so a handler can only act as the authenticated
// principal, in the principal's collection.
type call struct {
	context.Context
	p    *Principal
	s    *Server
	auth services.Context
}

func (s *Server) newCall(ctx context.Context, p *Principal) *call {
	return &call{
		Context: ctx,
		p:       p,
		s:       s,
		auth:    services.Context{Context: ctx, UID: p.User.ID, GID: p.GroupID, User: p.User},
	}
}

func (c *call) repos() *repo.AllRepos { return c.s.deps.Repos }
func (c *call) gid() uuid.UUID        { return c.p.GroupID }

// toolDef describes one tool. scope is the permission the principal must hold
// for the tool to be listed at all.
type toolDef[In, Out any] struct {
	name, title, description string
	scope                    string
	// kind selects the MCP behaviour hints clients use to decide whether to ask
	// the user before running the tool.
	kind    toolKind
	handler func(*call, In) (Out, error)
}

type toolKind int

const (
	kindRead        toolKind = iota // changes nothing
	kindCreate                      // adds data, never overwrites
	kindUpdate                      // modifies existing data; repeating it is harmless
	kindDestructive                 // removes data
)

func (k toolKind) annotations(title string) *mcp.ToolAnnotations {
	f, t := false, true
	a := &mcp.ToolAnnotations{Title: title, OpenWorldHint: &f}
	switch k {
	case kindRead:
		a.ReadOnlyHint = true
	case kindCreate:
		a.DestructiveHint = &f
	case kindUpdate:
		a.DestructiveHint = &f
		a.IdempotentHint = true
	case kindDestructive:
		a.DestructiveHint = &t
	}
	return a
}

// addTool registers a tool if, and only if, the principal holds its scope. A
// client therefore never sees tools it could not use, and a read-only
// credential cannot even name a write tool.
func addTool[In, Out any](s *Server, srv *mcp.Server, p *Principal, def toolDef[In, Out]) {
	if !hasScope(p, def.scope) {
		return
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        def.name,
		Title:       def.title,
		Description: def.description,
		Annotations: def.kind.annotations(def.title),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		out, err := def.handler(s.newCall(ctx, p), in)
		if err != nil {
			return nil, zero, sanitize(err)
		}
		return nil, out, nil
	})
}

func hasScope(p *Principal, scope string) bool {
	if scope == "" {
		return true
	}
	for _, s := range p.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

func (s *Server) registerTools(srv *mcp.Server, p *Principal) {
	registerReadTools(s, srv, p)
	registerWriteTools(s, srv, p)
}

// ---- errors ----

// userError is shown to the model verbatim. Anything else is logged and replaced
// by a generic message, so internals never reach the client.
type userError struct{ msg string }

func (e *userError) Error() string { return e.msg }

func invalid(format string, a ...any) error { return &userError{msg: fmt.Sprintf(format, a...)} }

func sanitize(err error) error {
	var ue *userError
	var re *validate.RequestError
	switch {
	case errors.As(err, &ue):
		return err
	case ent.IsNotFound(err):
		return &userError{msg: "not found"}
	case errors.As(err, &re) && re.Status >= 400 && re.Status < 500:
		return &userError{msg: re.Error()}
	case ent.IsConstraintError(err), ent.IsValidationError(err):
		return &userError{msg: "the change was rejected: " + firstLine(err.Error())}
	default:
		log.Err(err).Msg("mcp: tool failed")
		return &userError{msg: "internal error"}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return clip(s, 200)
}

func toolErrorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
}

// ---- argument parsing ----

func parseID(field, v string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(v))
	if err != nil {
		return uuid.Nil, invalid("%s must be a UUID", field)
	}
	return id, nil
}

func parseOptionalID(field, v string) (uuid.UUID, error) {
	if strings.TrimSpace(v) == "" {
		return uuid.Nil, nil
	}
	return parseID(field, v)
}

func parseIDs(field string, vs []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(vs))
	for _, v := range vs {
		id, err := parseID(field, v)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// parseDate accepts YYYY-MM-DD, the only date format tools document.
func parseDate(field, v string) (types.Date, error) {
	if strings.TrimSpace(v) == "" {
		return types.Date{}, nil
	}
	t, err := time.Parse("2006-01-02", strings.TrimSpace(v))
	if err != nil {
		return types.Date{}, invalid("%s must be a date in YYYY-MM-DD format", field)
	}
	return types.DateFromTime(t), nil
}

func requireConfirm(confirm bool, what string) error {
	if !confirm {
		return invalid("this permanently deletes %s. Ask the user to confirm this specific deletion, then call again with confirm=true", what)
	}
	return nil
}

// page normalises paging arguments against the configured cap.
func (c *call) page(page, size int) (int, int) {
	limit := c.s.deps.Conf.MaxPageSize
	if limit <= 0 {
		limit = 50
	}
	if size <= 0 {
		size = 25
	}
	return max(page, 1), min(size, limit)
}

// ---- output shaping ----

// clip truncates untrusted free text so one huge note can't flood the model's
// context, marking the cut so the model knows the text is incomplete.
func clip(s string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	r := []rune(s)
	return string(r[:limit]) + fmt.Sprintf("… [truncated, %d more characters]", len(r)-limit)
}

func (c *call) text(s string) string { return clip(s, c.s.deps.Conf.MaxTextLength) }

// Ref is a compact pointer to another record.
type Ref struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ItemSummary is the compact form returned by searches and lists.
type ItemSummary struct {
	ID            string  `json:"id"`
	AssetID       string  `json:"assetId,omitempty"       jsonschema:"human-friendly asset number, e.g. 000-123"`
	Name          string  `json:"name"`
	Description   string  `json:"description,omitempty"`
	Kind          string  `json:"kind"                    jsonschema:"item or location"`
	Type          string  `json:"type,omitempty"          jsonschema:"entity type name"`
	Quantity      float64 `json:"quantity"`
	Parent        *Ref    `json:"parent,omitempty"`
	Tags          []Ref   `json:"tags"`
	Archived      bool    `json:"archived,omitempty"`
	PurchasePrice float64 `json:"purchasePrice,omitempty"`
	ItemCount     float64 `json:"itemCount,omitempty"     jsonschema:"for locations: items stored inside"`
	UpdatedAt     string  `json:"updatedAt"`
}

func (c *call) summary(e repo.EntitySummary) ItemSummary {
	out := ItemSummary{
		ID:            e.ID.String(),
		Name:          e.Name,
		Description:   c.text(e.Description),
		Kind:          "item",
		Quantity:      e.Quantity,
		Tags:          refsFromTags(e.Tags),
		Archived:      e.Archived,
		PurchasePrice: e.PurchasePrice,
		ItemCount:     e.ItemCount,
		UpdatedAt:     e.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if !e.AssetID.Nil() {
		out.AssetID = e.AssetID.String()
	}
	if e.EntityType != nil {
		out.Type = e.EntityType.Name
		if e.EntityType.IsLocation {
			out.Kind = "location"
		}
	}
	if e.Parent != nil {
		out.Parent = &Ref{ID: e.Parent.ID.String(), Name: e.Parent.Name}
	}
	return out
}

func refsFromTags(tags []repo.TagSummary) []Ref {
	out := make([]Ref, 0, len(tags))
	for _, t := range tags {
		out = append(out, Ref{ID: t.ID.String(), Name: t.Name})
	}
	return out
}

type CustomField struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

type AttachmentInfo struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Type    string `json:"type"`
	Primary bool   `json:"primary,omitempty"`
}

// ItemDetail is the full record. Attachment contents are never included, only
// their metadata.
type ItemDetail struct {
	ID            string  `json:"id"`
	AssetID       string  `json:"assetId,omitempty"       jsonschema:"human-friendly asset number, e.g. 000-123"`
	Name          string  `json:"name"`
	Description   string  `json:"description,omitempty"`
	Kind          string  `json:"kind"                    jsonschema:"item or location"`
	Type          string  `json:"type,omitempty"          jsonschema:"entity type name"`
	Quantity      float64 `json:"quantity"`
	Parent        *Ref    `json:"parent,omitempty"`
	Tags          []Ref   `json:"tags"`
	Archived      bool    `json:"archived,omitempty"`
	PurchasePrice float64 `json:"purchasePrice,omitempty"`
	UpdatedAt     string  `json:"updatedAt"`

	Location         *Ref             `json:"location,omitempty"         jsonschema:"resolved location: own, or nearest location ancestor"`
	SerialNumber     string           `json:"serialNumber,omitempty"`
	ModelNumber      string           `json:"modelNumber,omitempty"`
	Manufacturer     string           `json:"manufacturer,omitempty"`
	Insured          bool             `json:"insured,omitempty"`
	LifetimeWarranty bool             `json:"lifetimeWarranty,omitempty"`
	WarrantyExpires  string           `json:"warrantyExpires,omitempty"`
	WarrantyDetails  string           `json:"warrantyDetails,omitempty"`
	PurchaseDate     string           `json:"purchaseDate,omitempty"`
	PurchaseFrom     string           `json:"purchaseFrom,omitempty"`
	SoldDate         string           `json:"soldDate,omitempty"`
	SoldTo           string           `json:"soldTo,omitempty"`
	SoldPrice        float64          `json:"soldPrice,omitempty"`
	Notes            string           `json:"notes,omitempty"`
	Fields           []CustomField    `json:"customFields,omitempty"`
	Attachments      []AttachmentInfo `json:"attachments,omitempty"`
	Children         []ItemSummary    `json:"children,omitempty"         jsonschema:"for locations: sub-locations directly inside, capped. Items inside are listed with search_items parentId"`
	ChildrenTotal    int              `json:"childrenTotal,omitempty"`
}

const maxChildren = 50

func (c *call) detail(e repo.EntityOut) ItemDetail {
	sum := c.summary(e.EntitySummary)
	d := ItemDetail{
		ID: sum.ID, AssetID: sum.AssetID, Name: sum.Name, Description: sum.Description, Kind: sum.Kind,
		Type: sum.Type, Quantity: sum.Quantity, Parent: sum.Parent, Tags: sum.Tags, Archived: sum.Archived,
		PurchasePrice: sum.PurchasePrice, UpdatedAt: sum.UpdatedAt,
		SerialNumber:     e.SerialNumber,
		ModelNumber:      e.ModelNumber,
		Manufacturer:     e.Manufacturer,
		Insured:          e.Insured,
		LifetimeWarranty: e.LifetimeWarranty,
		WarrantyExpires:  e.WarrantyExpires.String(),
		WarrantyDetails:  c.text(e.WarrantyDetails),
		PurchaseDate:     e.PurchaseDate.String(),
		PurchaseFrom:     e.PurchaseFrom,
		SoldDate:         e.SoldDate.String(),
		SoldTo:           e.SoldTo,
		SoldPrice:        e.SoldPrice,
		Notes:            c.text(e.Notes),
	}
	if e.Location != nil {
		d.Location = &Ref{ID: e.Location.ID.String(), Name: e.Location.Name}
	}
	for _, f := range e.Fields {
		v := f.TextValue
		switch f.Type {
		case "number":
			v = strconv.Itoa(f.NumberValue)
		case "boolean":
			v = strconv.FormatBool(f.BooleanValue)
		}
		d.Fields = append(d.Fields, CustomField{Name: f.Name, Type: f.Type, Value: c.text(v)})
	}
	for _, a := range e.Attachments {
		d.Attachments = append(d.Attachments, AttachmentInfo{ID: a.ID.String(), Title: a.Title, Type: a.Type, Primary: a.Primary})
	}
	d.ChildrenTotal = len(e.Children)
	for i, ch := range e.Children {
		if i == maxChildren {
			break
		}
		d.Children = append(d.Children, c.summary(ch))
	}
	return d
}

var _ = http.StatusOK
