package lifecycle

import (
	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/explicitgroup"
	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/explorer/population"
	"github.com/calypr/loom/internal/explorer/proposal"
	"github.com/calypr/loom/internal/explorer/publication"
	"github.com/calypr/loom/internal/explorer/rowdef"
	"github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/explorer/semanticbrowse"
)

type OwnerServices struct {
	Core           *authoringcore.Core
	Construction   *construction.Service
	ExplicitGroup  *explicitgroup.Service
	Interpretation *interpretation.Service
	Population     *population.Service
	Proposal       *proposal.Service
	Publication    *publication.Service
	RowDefinition  *rowdef.Service
	Selection      *selection.Service
	SemanticBrowse *semanticbrowse.Service
}

func (s *Service) OwnerServices() OwnerServices {
	return OwnerServices{
		Core: s.core, Construction: s.constructionService, ExplicitGroup: s.explicitGroupService,
		Interpretation: s.interpretationService, Population: s.populationService, Proposal: s.proposalService,
		Publication: s.publicationService, RowDefinition: s.rowDefService, Selection: s.selectionService,
		SemanticBrowse: s.browseService(),
	}
}
