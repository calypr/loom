package compilerfixture

import "github.com/calypr/loom/internal/dataframe"

func validBuilder() dataframe.Builder {
	return dataframe.Builder{Project: "p", RootResourceType: "Patient"}
}
