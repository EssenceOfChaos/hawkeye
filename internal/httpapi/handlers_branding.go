package httpapi

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"hawkeye/internal/branding"
	"hawkeye/internal/monitors"
	"hawkeye/internal/plans"
	"hawkeye/internal/reportpdf"
	"hawkeye/internal/reports"
	"hawkeye/internal/validation"
)

// brandingView is the settings page's data: what is stored, and what the plan lets this organization do with it.
func (h *handlers) brandingView(c echo.Context) error {
	ctx := c.Request().Context()
	b, err := h.branding.Get(ctx, currentOrg(c))
	if err != nil {
		return err
	}
	p, err := plans.EffectivePlan(ctx, h.db, currentOrg(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"branding": b, "can_report": p.ReportsEnabled, "can_brand": p.BrandedReportsEnabled})
}

// putBranding changes the display name and/or the monthly email switch. Each is optional, so the page can save one without sending the other.
// Clearing the name is always allowed; setting one needs a plan with branded reports, and turning the monthly email on needs one with reports.
func (h *handlers) putBranding(c echo.Context) error {
	var in struct {
		DisplayName        *string `json:"display_name"`
		MonthlyReportEmail *bool   `json:"monthly_report_email"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	ctx, org := c.Request().Context(), currentOrg(c)
	if in.DisplayName != nil {
		name, err := branding.CleanName(*in.DisplayName)
		if err != nil {
			return mapError(err)
		}
		if name != "" {
			if err := h.plans.CanUseBrandedReports(ctx, h.db, org); err != nil {
				return mapError(err)
			}
		}
		if err := h.branding.SaveName(ctx, org, name); err != nil {
			return mapError(err)
		}
	}
	if in.MonthlyReportEmail != nil {
		if *in.MonthlyReportEmail {
			if err := h.plans.CanGenerateReports(ctx, h.db, org); err != nil {
				return mapError(err)
			}
		}
		if err := h.branding.SetMonthlyEmail(ctx, org, *in.MonthlyReportEmail); err != nil {
			return mapError(err)
		}
	}
	return h.brandingView(c)
}

// putLogo takes the image itself as the request body (not a form), so the browser can send the chosen file as it is.
func (h *handlers) putLogo(c echo.Context) error {
	ctx, org := c.Request().Context(), currentOrg(c)
	if err := h.plans.CanUseBrandedReports(ctx, h.db, org); err != nil {
		return mapError(err)
	}
	raw, err := io.ReadAll(http.MaxBytesReader(c.Response(), c.Request().Body, branding.MaxLogoBytes+1))
	if err != nil {
		return mapError(validation.New("logo", "is too large: use an image under 512 KB"))
	}
	if err := h.branding.SaveLogo(ctx, org, raw); err != nil {
		return mapError(err)
	}
	return h.brandingView(c)
}

func (h *handlers) deleteLogo(c echo.Context) error {
	if err := h.branding.DeleteLogo(c.Request().Context(), currentOrg(c)); err != nil {
		return err
	}
	return h.brandingView(c)
}

func (h *handlers) getLogo(c echo.Context) error {
	data, ctype, err := h.branding.Logo(c.Request().Context(), currentOrg(c))
	if errors.Is(err, branding.ErrNoLogo) {
		return &APIError{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "No logo has been uploaded."}
	}
	if err != nil {
		return err
	}
	return c.Blob(http.StatusOK, ctype, data)
}

// monitorReportPDF returns one website's monthly report as a PDF download. With no month it is the last full month. Branding is applied only when the
// plan includes it, so after a downgrade the same link simply gives the standard HawkEye report.
func (h *handlers) monitorReportPDF(c echo.Context) error {
	id, err := h.monitorInScope(c)
	if err != nil {
		return err
	}
	ctx, org := c.Request().Context(), currentOrg(c)
	if err := h.plans.CanGenerateReports(ctx, h.db, org); err != nil {
		return mapError(err)
	}
	p, err := plans.EffectivePlan(ctx, h.db, org)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	month := reports.MonthStart(now).AddDate(0, -1, 0)
	if v := c.QueryParam("month"); v != "" {
		if month, err = reports.ParseMonth(v); err != nil {
			return mapError(err)
		}
	}
	m, err := reports.BuildMonthly(ctx, h.db, org, id, month, now, p.HistoryRetentionDays)
	if errors.Is(err, reports.ErrMonitorNotFound) {
		return mapError(monitors.ErrNotFound)
	}
	if err != nil {
		return mapError(err)
	}
	brand, err := h.branding.Brand(ctx, org, p.BrandedReportsEnabled)
	if err != nil {
		return err
	}
	pdf, err := reportpdf.Render(m, brand)
	if err != nil {
		return err
	}
	c.Response().Header().Set("Content-Disposition", `attachment; filename="`+reportpdf.Filename(m.Name, m.Month)+`"`)
	return c.Blob(http.StatusOK, "application/pdf", pdf)
}
