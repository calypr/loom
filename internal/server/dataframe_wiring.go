package server

import (
	dataframeapi "github.com/calypr/loom/internal/api/dataframe/dataframe"
	queryapi "github.com/calypr/loom/internal/api/dataframe/query"
)

type dataframeReadRoutes struct {
	published *dataframeapi.Service
	query     *queryapi.Service
}
