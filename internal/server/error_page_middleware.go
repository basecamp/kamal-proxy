package server

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
)

var contextKeyErrorResponse = contextKey("error-response")

type errorResponse struct {
	StatusCode        int
	TemplateArguments any
}

type ErrorPageMiddleware struct {
	template *template.Template
	root     bool
	next     http.Handler
}

func SetErrorResponse(w http.ResponseWriter, r *http.Request, statusCode int, templateArguments any) {
	errorResp, ok := r.Context().Value(contextKeyErrorResponse).(*errorResponse)
	if ok {
		errorResp.StatusCode = statusCode
		errorResp.TemplateArguments = templateArguments
	} else {
		// Fallback in case no middleware is present in the chain
		http.Error(w, http.StatusText(statusCode), statusCode)
	}
}

func WithErrorPageMiddleware(pages fs.FS, root bool, next http.Handler) http.Handler {
	template, err := template.ParseFS(pages, "*.html")
	if err != nil {
		slog.Error("Failed to parse error page templates", "error", err)
		template = nil
	}

	return &ErrorPageMiddleware{
		template: template,
		root:     root,
		next:     next,
	}
}

func (h *ErrorPageMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	errorResp, ok := r.Context().Value(contextKeyErrorResponse).(*errorResponse)
	if !ok {
		errorResp = &errorResponse{}
		ctx := context.WithValue(r.Context(), contextKeyErrorResponse, errorResp)
		r = r.WithContext(ctx)
	}

	h.next.ServeHTTP(w, r)

	if errorResp.StatusCode != 0 {
		handled := h.respondWithErrorPage(w, errorResp.StatusCode, errorResp.TemplateArguments)
		if handled {
			errorResp.StatusCode = 0
		}
	}
}

// Private

func (h *ErrorPageMiddleware) respondWithErrorPage(w http.ResponseWriter, statusCode int, templateArguments any) bool {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)

	template := h.getTemplate(statusCode)
	if template == nil {
		return h.writeErrorWithoutTemplate(w, statusCode)
	}

	err := template.Execute(w, templateArguments)
	if err != nil {
		slog.Error("Failed to render error page template", "name", template.Name, "error", err)
		return h.writeErrorWithoutTemplate(w, statusCode)
	}

	return true
}

func (h *ErrorPageMiddleware) getTemplate(statusCode int) *template.Template {
	if h.template == nil {
		return nil
	}

	return h.template.Lookup(fmt.Sprintf("%d.html", statusCode))
}

func (h *ErrorPageMiddleware) writeErrorWithoutTemplate(w http.ResponseWriter, statusCode int) bool {
	if h.root {
		// Only do this when we're the root middleware. Otherwise, we can let our parent try to handle it.
		fmt.Fprintf(w, "<h1>%d %s</h1>", statusCode, http.StatusText(statusCode))
		return true
	}

	return false
}
