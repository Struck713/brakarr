package controllers

import (
	"github.com/a-h/templ"
	"github.com/labstack/echo/v5"
)

// Render writes a templ component as an HTML response.
func Render(c *echo.Context, statusCode int, t templ.Component) error {
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	c.Response().WriteHeader(statusCode)
	return t.Render(c.Request().Context(), c.Response())
}
