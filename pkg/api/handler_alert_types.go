package api

import (
	"net/http"
	"sort"

	echo "github.com/labstack/echo/v5"
)

// AlertTypesResponse is returned by GET /api/v1/alert-types.
type AlertTypesResponse struct {
	AlertTypes       []AlertTypeInfo `json:"alert_types"`
	DefaultChainID   string          `json:"default_chain_id"`
	DefaultAlertType string          `json:"default_alert_type"`
}

// AlertTypeInfo describes a single alert type and its associated chain.
type AlertTypeInfo struct {
	Type        string `json:"type"`
	ChainID     string `json:"chain_id"`
	Description string `json:"description"`
}

// alertTypesHandler handles GET /api/v1/alert-types.
func (s *Server) alertTypesHandler(c *echo.Context) error {
	chains := s.cfg.ChainRegistry.GetAll()

	var alertTypes []AlertTypeInfo
	defaultChainID := ""
	defaultAlertType := ""

	if s.cfg.Defaults != nil {
		defaultAlertType = s.cfg.Defaults.AlertType
	}

	// Sort chain IDs for deterministic output.
	chainIDs := make([]string, 0, len(chains))
	for id := range chains {
		chainIDs = append(chainIDs, id)
	}
	sort.Strings(chainIDs)

	for _, chainID := range chainIDs {
		chain := chains[chainID]
		for _, alertType := range chain.AlertTypes {
			alertTypes = append(alertTypes, AlertTypeInfo{
				Type:        alertType,
				ChainID:     chainID,
				Description: chain.Description,
			})
			if alertType == defaultAlertType {
				defaultChainID = chainID
			}
		}
	}

	// Fall back to first chain if configured default doesn't match any alert type.
	if defaultChainID == "" && len(chainIDs) > 0 {
		defaultChainID = chainIDs[0]
	}

	if alertTypes == nil {
		alertTypes = []AlertTypeInfo{}
	}

	return c.JSON(http.StatusOK, AlertTypesResponse{
		AlertTypes:       alertTypes,
		DefaultChainID:   defaultChainID,
		DefaultAlertType: defaultAlertType,
	})
}
