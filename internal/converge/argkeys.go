package converge

// argKeys is the one source of truth for the argument keys one journal op
// accepts: allowed is every key it may carry, required the subset that must
// be present and non-null. decodeArgs reads these declarations; nothing else
// lists an op's keys (BUG-1140 r5).
type argKeys struct {
	allowed  []string
	required []string
}

var (
	// finance/sample
	argKeysFinanceSample = argKeys{allowed: nil, required: nil}
	// finance/post
	argKeysFinancePost = argKeys{allowed: []string{"description", "entries"}, required: []string{"entries"}}
	// finance/post_entry
	argKeysFinancePostEntry = argKeys{allowed: []string{"account", "side", "amount", "category"}, required: []string{"account", "side", "amount"}}
	// finance/begin_month
	argKeysFinanceBeginMonth = argKeys{allowed: []string{"month"}, required: []string{"month"}}
	// finance/post_wages
	argKeysFinancePostWages = argKeys{allowed: []string{"total"}, required: []string{"total"}}
	// finance/post_household_spend
	argKeysFinancePostHouseholdSpend = argKeys{allowed: []string{"quantity", "price"}, required: []string{"quantity", "price"}}
	// finance/settle_opex
	argKeysFinanceSettleOpex = argKeys{allowed: []string{"opex"}, required: []string{"opex"}}
	// finance/settle_construction
	argKeysFinanceSettleConstruction = argKeys{allowed: []string{"cost"}, required: []string{"cost"}}
	// finance/service_debt
	argKeysFinanceServiceDebt = argKeys{allowed: []string{"interest", "principal"}, required: []string{"interest", "principal"}}
	// population/seed
	argKeysPopulationSeed = argKeys{allowed: []string{"count", "ageYears"}, required: []string{"count", "ageYears"}}
	// population/advance
	argKeysPopulationAdvance = argKeys{allowed: []string{"days"}, required: []string{"days"}}
	// population/sample
	argKeysPopulationSample = argKeys{allowed: nil, required: nil}
	// services/place_service
	argKeysServicesPlaceService = argKeys{allowed: []string{"buildingID", "goKind", "goServiceID", "tsSpec", "x", "y"}, required: []string{"buildingID", "goKind", "goServiceID"}}
	// services/set_population
	argKeysServicesSetPopulation = argKeys{allowed: []string{"n"}, required: []string{"n"}}
	// services/sample
	argKeysServicesSample = argKeys{allowed: nil, required: nil}
)
