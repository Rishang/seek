package main

import (
	"net/http"
)

// This file serves the API documentation for `seek serve`:
//   GET /openapi.json  → the OpenAPI 3.0 spec (below)
//   GET /docs          → Swagger UI rendered against that spec
//
// Both are intentionally unauthenticated — they expose no secrets, only the
// shape of the API. The operation endpoints still require the Bearer token.
// ponytail: the spec is a hand-maintained string rather than generated from
// reflection; it's small and changes rarely. Keep it in sync with serve.go.

func handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(openAPISpec))
}

func handleSwaggerUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerUIHTML))
}

// swaggerUIHTML loads Swagger UI from a CDN and points it at /openapi.json.
const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>seek API — Swagger UI</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.ui = SwaggerUIBundle({
      url: "/openapi.json",
      dom_id: "#swagger-ui",
      deepLinking: true
    });
  </script>
</body>
</html>`

// openAPISpec is the OpenAPI 3.0 document describing the serve endpoints.
const openAPISpec = `{
  "openapi": "3.0.3",
  "info": {
    "title": "seek HTTP API",
    "description": "Web search, scrape, and crawl across pluggable providers with automatic failover.",
    "version": "0.1.0"
  },
  "servers": [{ "url": "/" }],
  "components": {
    "securitySchemes": {
      "bearerAuth": { "type": "http", "scheme": "bearer" }
    },
    "schemas": {
      "SearchRequest": {
        "type": "object",
        "required": ["query"],
        "properties": {
          "query": { "type": "string", "description": "Search query" },
          "provider": { "type": "string", "description": "Override provider (default: configured/auto)" },
          "range": { "type": "integer", "description": "Only results from the last N days" },
          "start": { "type": "string", "description": "Earliest published date, DD/MM/YYYY" },
          "end": { "type": "string", "description": "Latest published date, DD/MM/YYYY" }
        }
      },
      "SearchResult": {
        "type": "object",
        "properties": {
          "title": { "type": "string" },
          "url": { "type": "string" },
          "snippet": { "type": "string" },
          "published_date": { "type": "string" }
        }
      },
      "SearchResponse": {
        "type": "object",
        "properties": {
          "results": { "type": "array", "items": { "$ref": "#/components/schemas/SearchResult" } }
        }
      },
      "ScrapeRequest": {
        "type": "object",
        "required": ["url"],
        "properties": {
          "url": { "type": "string" },
          "provider": { "type": "string" },
          "format": { "type": "string", "enum": ["markdown", "html", "json"] }
        }
      },
      "ScrapeResult": {
        "type": "object",
        "properties": {
          "url": { "type": "string" },
          "content": { "type": "string" },
          "format": { "type": "string" }
        }
      },
      "CrawlRequest": {
        "type": "object",
        "required": ["url"],
        "properties": {
          "url": { "type": "string" },
          "provider": { "type": "string" }
        }
      },
      "CrawlResult": {
        "type": "object",
        "properties": {
          "url": { "type": "string" },
          "pages": { "type": "array", "items": { "type": "string" } },
          "content": { "type": "string" }
        }
      },
      "Error": {
        "type": "object",
        "properties": { "error": { "type": "string" } }
      }
    }
  },
  "security": [{ "bearerAuth": [] }],
  "paths": {
    "/search": {
      "post": {
        "summary": "Run a web search",
        "requestBody": {
          "required": true,
          "content": { "application/json": { "schema": { "$ref": "#/components/schemas/SearchRequest" } } }
        },
        "responses": {
          "200": { "description": "Results", "content": { "application/json": { "schema": { "$ref": "#/components/schemas/SearchResponse" } } } },
          "400": { "description": "Invalid request", "content": { "application/json": { "schema": { "$ref": "#/components/schemas/Error" } } } },
          "401": { "description": "Unauthorized" },
          "502": { "description": "Provider error", "content": { "application/json": { "schema": { "$ref": "#/components/schemas/Error" } } } }
        }
      }
    },
    "/scrape": {
      "post": {
        "summary": "Extract a single page",
        "requestBody": {
          "required": true,
          "content": { "application/json": { "schema": { "$ref": "#/components/schemas/ScrapeRequest" } } }
        },
        "responses": {
          "200": { "description": "Scraped page", "content": { "application/json": { "schema": { "$ref": "#/components/schemas/ScrapeResult" } } } },
          "400": { "description": "Invalid request", "content": { "application/json": { "schema": { "$ref": "#/components/schemas/Error" } } } },
          "401": { "description": "Unauthorized" },
          "502": { "description": "Provider error", "content": { "application/json": { "schema": { "$ref": "#/components/schemas/Error" } } } }
        }
      }
    },
    "/crawl": {
      "post": {
        "summary": "Crawl a website",
        "requestBody": {
          "required": true,
          "content": { "application/json": { "schema": { "$ref": "#/components/schemas/CrawlRequest" } } }
        },
        "responses": {
          "200": { "description": "Crawl result", "content": { "application/json": { "schema": { "$ref": "#/components/schemas/CrawlResult" } } } },
          "400": { "description": "Invalid request", "content": { "application/json": { "schema": { "$ref": "#/components/schemas/Error" } } } },
          "401": { "description": "Unauthorized" },
          "502": { "description": "Provider error", "content": { "application/json": { "schema": { "$ref": "#/components/schemas/Error" } } } }
        }
      }
    },
    "/healthz": {
      "get": {
        "summary": "Liveness check",
        "security": [],
        "responses": { "200": { "description": "ok" } }
      }
    }
  }
}`
