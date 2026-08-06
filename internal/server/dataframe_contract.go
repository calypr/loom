package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

const dataframeContractsCollection = "loom_dataframe_contracts"

var errNoDataframeContract = errors.New("no promoted dataframe contract")

type promotedDataframeContract struct {
	Recipe             string    `json:"recipe"`
	TranslationVersion string    `json:"translationVersion"`
	PromotedAt         time.Time `json:"promotedAt"`
}

type dataframeContractState struct {
	mu       sync.RWMutex
	contract promotedDataframeContract
}

func (s *dataframeContractState) Set(contract promotedDataframeContract) {
	s.mu.Lock()
	s.contract = contract
	s.mu.Unlock()
}

func (s *dataframeContractState) Current() (string, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.contract.Recipe, s.contract.TranslationVersion
}

type dataframeContractStore struct{ query releaseQueryClient }

func dataframeContractBootstrapSpec() arangostore.BootstrapSpec {
	return arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{{Name: dataframeContractsCollection}}}
}

func (s dataframeContractStore) Load(ctx context.Context) (promotedDataframeContract, error) {
	if s.query == nil {
		return promotedDataframeContract{}, fmt.Errorf("dataframe contract store is unavailable")
	}
	var result *promotedDataframeContract
	err := s.query.QueryRows(ctx, `LET contract = DOCUMENT(@@collection, "default") FILTER contract != null RETURN contract`, 1, map[string]interface{}{"@collection": dataframeContractsCollection}, func(row map[string]any) error {
		contract := promotedDataframeContract{}
		contract.Recipe, _ = row["recipe"].(string)
		contract.TranslationVersion, _ = row["translationVersion"].(string)
		switch value := row["promotedAt"].(type) {
		case time.Time:
			contract.PromotedAt = value
		case string:
			parsed, parseErr := time.Parse(time.RFC3339Nano, value)
			if parseErr != nil {
				return parseErr
			}
			contract.PromotedAt = parsed
		}
		if strings.TrimSpace(contract.Recipe) == "" || strings.TrimSpace(contract.TranslationVersion) == "" {
			return fmt.Errorf("stored dataframe contract is invalid")
		}
		result = &contract
		return nil
	})
	if err != nil {
		return promotedDataframeContract{}, err
	}
	if result == nil {
		return promotedDataframeContract{}, errNoDataframeContract
	}
	return *result, nil
}

func (s dataframeContractStore) Promote(ctx context.Context, recipe, version string) (promotedDataframeContract, error) {
	recipe, version = strings.TrimSpace(recipe), strings.TrimSpace(version)
	if s.query == nil || recipe == "" || version == "" {
		return promotedDataframeContract{}, fmt.Errorf("recipe and translation version are required")
	}
	candidate := promotedDataframeContract{Recipe: recipe, TranslationVersion: version, PromotedAt: time.Now().UTC()}
	var result *promotedDataframeContract
	err := s.query.QueryRows(ctx, `UPSERT {_key: "default"}
INSERT {_key: "default", recipe: @recipe, translationVersion: @version, promotedAt: @promotedAt}
UPDATE {recipe: @recipe, translationVersion: @version, promotedAt: @promotedAt}
IN @@collection
RETURN NEW`, 1, map[string]interface{}{
		"@collection": dataframeContractsCollection, "recipe": recipe,
		"version": version, "promotedAt": candidate.PromotedAt,
	}, func(map[string]any) error {
		copy := candidate
		result = &copy
		return nil
	})
	if err != nil {
		return promotedDataframeContract{}, err
	}
	if result == nil {
		return promotedDataframeContract{}, fmt.Errorf("promote dataframe contract returned no result")
	}
	return *result, nil
}
