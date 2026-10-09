package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// auditMiddleware logs every tool call and resource read, and rejects results
// larger than the configured cap.
//
// Arguments are never logged: they can hold personal data and free text. The
// log records who (user, collection, credential), what (tool and argument
// names) and how it went, which is what an operator needs to answer "what did
// this assistant do".
func (s *Server) auditMiddleware(p *Principal) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" && method != "resources/read" {
				return next(ctx, method, req)
			}

			target, argNames := describeRequest(method, req)
			ctx, span := otel.Tracer("mcp").Start(ctx, "mcp."+method, trace.WithAttributes(
				attribute.String("user.id", p.User.ID.String()),
				attribute.String("group.id", p.GroupID.String()),
				attribute.String("mcp.credential.kind", p.CredentialKind),
				attribute.String("mcp.credential.id", p.CredentialID.String()),
				attribute.String("mcp.target", target),
			))
			defer span.End()

			start := time.Now()
			res, err := next(ctx, method, req)

			outcome := "ok"
			switch {
			case err != nil:
				outcome = "protocol_error"
			default:
				if ctr, ok := res.(*mcp.CallToolResult); ok {
					if ctr.IsError {
						outcome = "tool_error"
					} else if size := resultSize(ctr); s.deps.Conf.MaxResponseBytes > 0 && size > s.deps.Conf.MaxResponseBytes {
						outcome = "too_large"
						res = tooLarge(size, s.deps.Conf.MaxResponseBytes)
					}
				}
			}

			span.SetAttributes(attribute.String("mcp.outcome", outcome))
			if outcome != "ok" {
				span.SetStatus(codes.Error, outcome)
			}

			level := zerolog.InfoLevel
			if outcome != "ok" {
				level = zerolog.WarnLevel
			}
			log.WithLevel(level).Str("audit", "mcp").
				Str("method", method).
				Str("target", target).
				Strs("args", argNames).
				Str("outcome", outcome).
				Dur("duration", time.Since(start)).
				Str("user.id", p.User.ID.String()).
				Str("group.id", p.GroupID.String()).
				Str("credential.kind", p.CredentialKind).
				Str("credential.id", p.CredentialID.String()).
				Str("credential.name", p.CredentialName).
				Msg("mcp request")

			return res, err
		}
	}
}

func describeRequest(method string, req mcp.Request) (target string, argNames []string) {
	switch r := req.(type) {
	case *mcp.CallToolRequest:
		if r.Params == nil {
			return "", nil
		}
		target = r.Params.Name
		if raw, err := json.Marshal(r.Params.Arguments); err == nil {
			var m map[string]json.RawMessage
			if json.Unmarshal(raw, &m) == nil {
				for k := range m {
					argNames = append(argNames, k)
				}
				sort.Strings(argNames)
			}
		}
	case *mcp.ReadResourceRequest:
		if r.Params != nil {
			target = r.Params.URI
		}
	}
	return target, argNames
}

func resultSize(r *mcp.CallToolResult) int {
	b, err := json.Marshal(r)
	if err != nil {
		return 0
	}
	return len(b)
}

func tooLarge(size, limit int) *mcp.CallToolResult {
	return toolErrorResult(errors.New("the result is too large to return (" + itoa(size) + " bytes, limit " + itoa(limit) + "); narrow the query or request a smaller page"))
}
