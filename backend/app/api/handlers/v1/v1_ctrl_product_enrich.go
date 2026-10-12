package v1

import (
	"context"
	"net/http"
	"strings"

	"github.com/hay-kot/httpkit/errchain"
	"github.com/hay-kot/httpkit/server"
	"github.com/rs/zerolog/log"
	"github.com/sysadminsmedia/homebox/backend/internal/core/enrich"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/data/types"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
	"github.com/sysadminsmedia/homebox/backend/internal/web/adapters"
)

const enrichMaxResults = 5

func candidateToProduct(e types.LookupCandidate) repo.BarcodeProduct {
	var p repo.BarcodeProduct
	p.SearchEngineName = strings.Join(e.Sources, ", ")
	p.Item.Name = e.Name
	p.Item.Description = e.Description
	p.Manufacturer = e.Manufacturer
	p.ModelNumber = e.ModelNumber
	p.Country = e.Country
	p.Barcode = e.Barcode
	p.ImageURL = e.ImageURL
	p.SourceURL = e.SourceURL
	p.Confidence = e.Confidence
	return p
}

// lookupEnrich asks a scrapling-barcode service for candidates matching query.
func lookupEnrich(client *http.Client, conf config.BarcodeAPIConf, query string) ([]repo.BarcodeProduct, error) {
	result, err := enrich.Client{BaseURL: conf.EnrichURL, Token: conf.EnrichToken, HTTP: client}.
		Lookup(context.Background(), query, enrichMaxResults)
	if err != nil {
		return nil, err
	}
	for source, msg := range result.Errors {
		log.Warn().Msg("enrich source " + source + ": " + msg)
	}

	products := make([]repo.BarcodeProduct, 0, len(result.Candidates))
	for _, c := range result.Candidates {
		products = append(products, candidateToProduct(c))
	}
	return products, nil
}

// HandleProductEnrich godoc
//
//	@Summary	Search the web for product data (barcode or product name)
//	@Tags		Items
//	@Produce	json
//	@Param		q	query		string	true	"barcode or product name"
//	@Success	200	{object}	[]repo.BarcodeProduct
//	@Router		/v1/products/enrich [GET]
//	@Security	Bearer
func (ctrl *V1Controller) HandleProductEnrich(conf config.BarcodeAPIConf, notifier *config.NotifierConf) errchain.HandlerFunc {
	type query struct {
		Q string `schema:"q" validate:"required,max=200"`
	}

	// The service URL is operator configuration (usually on the local network), so it is
	// called directly. Image URLs come from arbitrary web pages, so they are downloaded
	// through the notifier SSRF policy instead.
	serviceClient := &http.Client{Timeout: conf.EnrichTimeout}
	imageClient := validate.NotifierHTTPClient(notifier)

	return func(w http.ResponseWriter, r *http.Request) error {
		if conf.EnrichURL == "" {
			return server.JSON(w, http.StatusNotFound, map[string]string{"error": "web enrichment is not configured"})
		}

		q, err := adapters.DecodeQuery[query](r)
		if err != nil {
			return err
		}

		// Browser-driven lookups can outlast the server's write deadline.
		allowSlowResponse(w, r)

		products, err := lookupEnrich(serviceClient, conf, q.Q)
		if err != nil {
			log.Error().Err(err).Msg("web enrichment failed")
			return server.JSON(w, http.StatusBadGateway, map[string]string{"error": "web enrichment service unavailable"})
		}

		for i := range products {
			p := &products[i]
			if !strings.HasPrefix(p.ImageURL, "https://") {
				p.ImageURL = ""
				continue
			}
			img, err := fetchImageBase64With(imageClient, p.ImageURL)
			if err != nil {
				log.Warn().Msg("Cannot fetch enrich image for URL: " + p.ImageURL + ": " + err.Error())
				p.ImageURL = ""
				continue
			}
			p.ImageBase64 = img
		}

		if len(products) == 0 {
			return server.JSON(w, http.StatusNoContent, nil)
		}
		return server.JSON(w, http.StatusOK, products)
	}
}
