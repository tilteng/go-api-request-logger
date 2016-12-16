package request_logger_mw

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tilteng/go-api-router/api_router"
	"github.com/tilteng/go-logger/logger"
)

type LogBodyFilterFn func(context.Context, []byte) []byte

func (self LogBodyFilterFn) FilterBody(ctx context.Context, bytes []byte) []byte {
	return self(ctx, bytes)
}

type LogBodyFilter interface {
	FilterBody(context.Context, []byte) []byte
}

type LogHeadersFilterFn func(context.Context, http.Header) http.Header

func (self LogHeadersFilterFn) FilterHeaders(ctx context.Context, hdrs http.Header) http.Header {
	return self(ctx, hdrs)
}

type LogHeadersFilter interface {
	FilterHeaders(context.Context, http.Header) http.Header
}

type RequestLoggerOpts struct {
	LogBodyFilter    LogBodyFilter
	LogHeadersFilter LogHeadersFilter
	Logger           logger.CtxLogger
	Disable          bool
}

type RequestLoggerMiddleware struct {
	opts *RequestLoggerOpts
}

func (self *RequestLoggerMiddleware) NewWrapper(ctx context.Context, opts ...interface{}) *RequestLoggerWrapper {
	var opt *RequestLoggerOpts

	for _, opt_map_i := range opts {
		var ok bool
		opt, ok = opt_map_i.(*RequestLoggerOpts)
		if ok {
			break
		}
	}

	if opt == nil {
		opt = &RequestLoggerOpts{
			Logger: self.opts.Logger,
		}
	}

	if opt.Disable {
		return nil
	}

	return &RequestLoggerWrapper{
		base_opts: self.opts,
		opts:      opt,
	}
}

type RequestLoggerWrapper struct {
	opts      *RequestLoggerOpts
	base_opts *RequestLoggerOpts
}

func (self *RequestLoggerMiddleware) SetLogger(logger logger.CtxLogger) *RequestLoggerMiddleware {
	self.opts.Logger = logger
	return self
}

func (self *RequestLoggerWrapper) filterBody(ctx context.Context, body []byte) []byte {
	// body is already a copy from the request, so no need to clone first
	if self.opts.LogBodyFilter != nil {
		body = self.opts.LogBodyFilter.FilterBody(ctx, body)
	}

	if self.base_opts.LogBodyFilter != nil {
		body = self.opts.LogBodyFilter.FilterBody(ctx, body)
	}

	return body
}

func (self *RequestLoggerWrapper) filterHeaders(ctx context.Context, hdrs http.Header) http.Header {
	n_hdrs := make(map[string][]string, len(hdrs))
	for k, v := range hdrs {
		n_hdrs[k] = make([]string, len(v), len(v))
		copy(n_hdrs[k], v)
	}

	if self.opts.LogHeadersFilter != nil {
		n_hdrs = self.opts.LogHeadersFilter.FilterHeaders(ctx, n_hdrs)
	}

	if self.base_opts.LogHeadersFilter != nil {
		n_hdrs = self.opts.LogHeadersFilter.FilterHeaders(ctx, n_hdrs)
	}

	return n_hdrs
}

func (self *RequestLoggerWrapper) Wrap(next api_router.RouteFn) api_router.RouteFn {
	return func(ctx context.Context) {
		rctx := api_router.RequestContextFromContext(ctx)
		rt := rctx.CurrentRoute()
		http_req := rctx.HTTPRequest()
		method := http_req.Method

		if self.opts.Logger == nil || self.opts.Disable {
			// No logging... just run the handler
			next(ctx)
			return
		}

		body, err := rctx.BodyCopy()
		if err != nil {
			panic(fmt.Sprintf("Couldn't read body: %+v", err))
		}

		// Copy the original URL -- This ensures the query string can't
		// be modified within the handler.
		orig_url := *http_req.URL

		body = self.filterBody(ctx, body)

		log_info := map[string]interface{}{
			"request": map[string]interface{}{
				"route": map[string]interface{}{
					"method": method,
					"route":  rt.FullPath(),
					"path":   orig_url.EscapedPath(),
					"query":  orig_url.Query(),
				},
				"headers": self.filterHeaders(ctx, http_req.Header),
				"body":    string(body),
			},
		}

		buf := bytes.NewBufferString("")
		encoder := json.NewEncoder(buf)
		encoder.SetEscapeHTML(false)
		encoder.Encode(log_info)

		self.opts.Logger.LogDebug(
			ctx, "Received request:", strings.TrimSpace(buf.String()),
		)

		next(ctx)

		writer := rctx.ResponseWriter()
		body = self.filterBody(ctx, writer.ResponseCopy())

		log_info["response"] = map[string]interface{}{
			"status":  writer.Status(),
			"headers": self.filterHeaders(ctx, writer.Header()),
			"body":    string(body),
		}

		buf.Reset()
		encoder.Encode(log_info)

		self.opts.Logger.LogDebug(
			ctx, "Sent response:", strings.TrimSpace(buf.String()),
		)
	}
}

func NewMiddleware(opts *RequestLoggerOpts) *RequestLoggerMiddleware {
	if opts == nil {
		opts = &RequestLoggerOpts{}
	}
	if opts.Logger == nil {
		opts.Logger = logger.DefaultStdoutCtxLogger()
	}
	return &RequestLoggerMiddleware{
		opts: opts,
	}
}
