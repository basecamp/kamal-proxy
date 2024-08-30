package server

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
)

var (
	//go:embed pages
	pages embed.FS

	contextKeyErrorResponse = contextKey("error-response")
)

type errorResponseContent struct {
	StatusCode        int
	TemplateArguments any
}

type ErrorPageMiddleware struct {
	template *template.Template
	next     http.Handler
}

func WithErrorPageMiddleware(next http.Handler) http.Handler {
	template, err := template.ParseFS(pages, "pages/*.html")
	if err != nil {
		slog.Error("Failed to parse error page templates", "error", err)
		template = nil
	}

	return &ErrorPageMiddleware{
		template: template,
		next:     next,
	}
}

func SetErrorResponse(w http.ResponseWriter, r *http.Request, statusCode int, templateArguments any) {
	errorResponse, ok := r.Context().Value(contextKeyErrorResponse).(*errorResponseContent)
	if ok {
		errorResponse.StatusCode = statusCode
		errorResponse.TemplateArguments = templateArguments
	} else {
		http.Error(w, http.StatusText(statusCode), statusCode)
	}
}

func (h *ErrorPageMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var errorResponseContext errorResponseContent
	ctx := context.WithValue(r.Context(), contextKeyErrorResponse, &errorResponseContext)
	r = r.WithContext(ctx)

	h.next.ServeHTTP(w, r)

	if errorResponseContext.StatusCode != 0 {
		h.respondWithErrorPage(w, errorResponseContext.StatusCode, errorResponseContext.TemplateArguments)
	}
}

// Private

func (h *ErrorPageMiddleware) respondWithErrorPage(w http.ResponseWriter, statusCode int, templateArguments any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)

	template := h.getTemplate(statusCode)
	if template == nil {
		slog.Error("Failed to render error page due to missing template", "status", statusCode)
		h.writeErrorWithoutTemplate(w, statusCode)
		return
	}

	err := template.Execute(w, templateArguments)
	if err != nil {
		slog.Error("Failed to render error page template", "name", template.Name, "error", err)
		h.writeErrorWithoutTemplate(w, statusCode)
	}
}

func (h *ErrorPageMiddleware) getTemplate(statusCode int) *template.Template {
	if h.template == nil {
		return nil
	}

	return h.template.Lookup(fmt.Sprintf("%d.html", statusCode))
}

func (h *ErrorPageMiddleware) writeErrorWithoutTemplate(w http.ResponseWriter, statusCode int) {
	fmt.Fprintf(w, "<h1>%d %s</h1>", statusCode, http.StatusText(statusCode))
}
