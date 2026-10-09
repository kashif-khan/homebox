package mcpserver

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sysadminsmedia/homebox/backend/internal/core/scopes"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
)

const resourceJSON = "application/json"

// registerResources exposes read-only reference data that clients can attach to a
// conversation without a tool call. They need items:read like the tools do.
func (s *Server) registerResources(srv *mcp.Server, p *Principal) {
	if !hasScope(p, scopes.ItemsRead) {
		return
	}

	srv.AddResource(&mcp.Resource{
		URI: "homebox://locations", Name: "locations", Title: "Top-level locations",
		Description: "The top-level locations of the collection." + untrusted, MIMEType: resourceJSON,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		c := s.newCall(ctx, p)
		t := true
		res, err := c.repos().Entities.QueryByGroup(c, c.gid(), repo.EntityQuery{
			IsLocation: &t, FilterChildren: true, Page: 1, PageSize: c.pageCap(), OrderBy: "name",
		})
		if err != nil {
			return nil, sanitize(err)
		}
		return jsonResource(req.Params.URI, c.itemList(res, 1, c.pageCap()))
	})

	srv.AddResource(&mcp.Resource{
		URI: "homebox://tags", Name: "tags", Title: "Tags",
		Description: "Every tag in the collection." + untrusted, MIMEType: resourceJSON,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		c := s.newCall(ctx, p)
		tags, err := c.repos().Tags.GetAll(c, c.gid())
		if err != nil {
			return nil, sanitize(err)
		}
		out := ListTagsOut{Tags: make([]TagInfo, 0, len(tags))}
		for _, t := range tags {
			out.Tags = append(out.Tags, c.tagInfo(t))
		}
		return jsonResource(req.Params.URI, out)
	})

	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "homebox://item/{id}", Name: "item", Title: "Item",
		Description: "One item or location by id." + untrusted, MIMEType: resourceJSON,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		c := s.newCall(ctx, p)
		id, err := uuid.Parse(strings.TrimPrefix(req.Params.URI, "homebox://item/"))
		if err != nil {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		e, err := c.repos().Entities.GetOneByGroup(c, c.gid(), id)
		if err != nil {
			if sanitize(err).Error() == "not found" {
				return nil, mcp.ResourceNotFoundError(req.Params.URI)
			}
			return nil, sanitize(err)
		}
		return jsonResource(req.Params.URI, c.detail(e))
	})
}

func (c *call) pageCap() int {
	if n := c.s.deps.Conf.MaxPageSize; n > 0 {
		return n
	}
	return 50
}

func jsonResource(uri string, v any) (*mcp.ReadResourceResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: resourceJSON, Text: string(b)}}}, nil
}
