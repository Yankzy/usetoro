package enrichment

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/google/uuid"
)

// Diacritics regex for Arabic tashkeel removal.
var arabicDiacriticsRegex = regexp.MustCompile("[\u064B-\u065F\u0670]")

// NormalizeArabicText strips tashkeel, standardizes Alef variants, Taa Marbuta, and Yaa.
func NormalizeArabicText(s string) string {
	// Strip tashkeel / harakat
	s = arabicDiacriticsRegex.ReplaceAllString(s, "")

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\u0622', '\u0623', '\u0625', '\u0671': // آ, أ, إ, ٱ -> ا
			b.WriteRune('\u0627')
		case '\u0629': // ة -> ه
			b.WriteRune('\u0647')
		case '\u0649': // ى -> ي
			b.WriteRune('\u064A')
		case '\u0640': // Tatweel (ـ) -> remove
			continue
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// DetectScriptType determines whether text is Arabic, Latin transliteration, acronym, or legal string.
func DetectScriptType(text string) ScriptType {
	hasArabic := false
	hasLatin := false

	for _, r := range text {
		if unicode.In(r, unicode.Arabic) {
			hasArabic = true
		} else if unicode.In(r, unicode.Latin) {
			hasLatin = true
		}
	}

	if hasArabic {
		return ScriptArabic
	}

	trimmed := strings.TrimSpace(text)
	if len(trimmed) <= 5 && !strings.Contains(trimmed, " ") {
		return ScriptAcronym
	}

	if hasLatin {
		return ScriptFrenchLegal
	}

	return ScriptBankAbbreviation
}

// NormalizeSearchStem standardizes text for fuzzy & exact matching.
func NormalizeSearchStem(text string) string {
	if DetectScriptType(text) == ScriptArabic {
		return NormalizeArabicText(text)
	}
	// Latin/French normalization: uppercase and strip extra whitespaces
	cleaned := strings.ToUpper(strings.TrimSpace(text))
	cleaned = strings.ReplaceAll(cleaned, "  ", " ")
	return cleaned
}

// AliasResolver holds an in-memory thread-safe lookup index for Moroccan merchants.
type AliasResolver struct {
	mu            sync.RWMutex
	exactMap      map[string]*MerchantAliasRecord
	merchantsByID map[uuid.UUID]*MasterMerchant
	merchantsByICE map[string]*MasterMerchant
}

// NewAliasResolver initializes the resolver and loads default seed entries into memory.
func NewAliasResolver() *AliasResolver {
	ar := &AliasResolver{
		exactMap:      make(map[string]*MerchantAliasRecord),
		merchantsByID: make(map[uuid.UUID]*MasterMerchant),
		merchantsByICE: make(map[string]*MasterMerchant),
	}
	ar.loadDefaultSeedData()
	return ar
}

// RegisterAlias inserts or updates an alias in the in-memory cache.
func (ar *AliasResolver) RegisterAlias(alias *MerchantAliasRecord) {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	normKey := NormalizeSearchStem(alias.AliasVariant)
	ar.exactMap[normKey] = alias

	if alias.Merchant != nil {
		ar.merchantsByID[alias.Merchant.ID] = alias.Merchant
		if alias.Merchant.ICE != "" {
			ar.merchantsByICE[alias.Merchant.ICE] = alias.Merchant
		}
	}
}

// ResolveMerchant finds a master merchant by exact alias, normalized stem, or ICE.
func (ar *AliasResolver) ResolveMerchant(ctx context.Context, stem string) (*MasterMerchant, *MatchedAliasInfo, bool) {
	ar.mu.RLock()
	defer ar.mu.RUnlock()

	norm := NormalizeSearchStem(stem)

	// 1. Direct normalized lookup
	if aliasRec, ok := ar.exactMap[norm]; ok && aliasRec.Merchant != nil {
		return aliasRec.Merchant, &MatchedAliasInfo{
			RawAlias:   aliasRec.AliasVariant,
			ScriptType: aliasRec.ScriptType,
			Language:   aliasRec.LanguageCode,
		}, true
	}

	// 2. Prefix / substring match for common stems
	for key, aliasRec := range ar.exactMap {
		if strings.Contains(norm, key) || (len(key) > 4 && strings.Contains(key, norm)) {
			if aliasRec.Merchant != nil {
				return aliasRec.Merchant, &MatchedAliasInfo{
					RawAlias:   aliasRec.AliasVariant,
					ScriptType: aliasRec.ScriptType,
					Language:   aliasRec.LanguageCode,
				}, true
			}
		}
	}

	return nil, nil, false
}

// ResolveByICE matches directly by 15-digit ICE.
func (ar *AliasResolver) ResolveByICE(ice string) (*MasterMerchant, bool) {
	ar.mu.RLock()
	defer ar.mu.RUnlock()

	m, ok := ar.merchantsByICE[ice]
	return m, ok
}

// loadDefaultSeedData populates initial master data for instantaneous in-memory execution.
func (ar *AliasResolver) loadDefaultSeedData() {
	// Helper to add merchant + aliases
	addEntry := func(m *MasterMerchant, aliases []struct {
		variant    string
		scriptType ScriptType
		lang       string
		primary    bool
	}) {
		ar.merchantsByID[m.ID] = m
		if m.ICE != "" {
			ar.merchantsByICE[m.ICE] = m
		}
		for _, a := range aliases {
			rec := &MerchantAliasRecord{
				ID:               uuid.New(),
				MasterMerchantID: m.ID,
				AliasVariant:     a.variant,
				ScriptType:       a.scriptType,
				LanguageCode:     a.lang,
				IsPrimary:        a.primary,
				ConfidenceScore:  1.00,
				Merchant:         m,
			}
			normKey := NormalizeSearchStem(a.variant)
			ar.exactMap[normKey] = rec
		}
	}

	// 1. Redal
	mRedal := &MasterMerchant{
		ID:                 uuid.MustParse("00000000-0000-0000-0001-000000000001"),
		NormalizedName:     "Redal S.A. (Veolia Maroc)",
		LegalName:          "Redal S.A.",
		CountryCode:        "MA",
		MerchantCategory:   "UTILITY_WATER_ELEC",
		ICE:                "001523456000089",
		IdentifiantFiscal:  "01085241",
		RegistreCommerce:   "42510_RABAT",
		CNSSNumber:         "1849204",
		PrimaryDomain:      "redal.ma",
		DefaultPCGMAccount: "614400",
		DefaultTVARate:     0.07,
		DefaultTVAAccount:  "345510",
		IsTVADeductible:    true,
		IsForeignService:   false,
		ConfidenceWeight:   1.00,
	}
	addEntry(mRedal, []struct {
		variant    string
		scriptType ScriptType
		lang       string
		primary    bool
	}{
		{"ريضال", ScriptArabic, "ar", true},
		{"REDAL", ScriptFrenchLegal, "fr", true},
		{"REDAL SA", ScriptFrenchLegal, "fr", false},
		{"VEOLIA MAROC", ScriptFrenchLegal, "fr", false},
	})

	// 2. Lydec
	mLydec := &MasterMerchant{
		ID:                 uuid.MustParse("00000000-0000-0000-0001-000000000002"),
		NormalizedName:     "Lydec (Lyonnaise des Eaux de Casablanca)",
		LegalName:          "Lydec S.A.",
		CountryCode:        "MA",
		MerchantCategory:   "UTILITY_WATER_ELEC",
		ICE:                "001511223000012",
		IdentifiantFiscal:  "01089944",
		RegistreCommerce:   "58210_CASABLANCA",
		CNSSNumber:         "1849999",
		PrimaryDomain:      "lydec.ma",
		DefaultPCGMAccount: "614400",
		DefaultTVARate:     0.07,
		DefaultTVAAccount:  "345510",
		IsTVADeductible:    true,
	}
	addEntry(mLydec, []struct {
		variant    string
		scriptType ScriptType
		lang       string
		primary    bool
	}{
		{"ليدك", ScriptArabic, "ar", true},
		{"LYDEC", ScriptFrenchLegal, "fr", true},
		{"LYDEC SA", ScriptFrenchLegal, "fr", false},
	})

	// 3. ONEE
	mONEE := &MasterMerchant{
		ID:                 uuid.MustParse("00000000-0000-0000-0001-000000000004"),
		NormalizedName:     "ONEE (Office National de l'Electricité et de l'Eau Potable)",
		LegalName:          "ONEE",
		CountryCode:        "MA",
		MerchantCategory:   "UTILITY_ELECTRICITY",
		ICE:                "001500000000001",
		PrimaryDomain:      "one.org.ma",
		DefaultPCGMAccount: "614400",
		DefaultTVARate:     0.14,
		DefaultTVAAccount:  "345510",
		IsTVADeductible:    true,
	}
	addEntry(mONEE, []struct {
		variant    string
		scriptType ScriptType
		lang       string
		primary    bool
	}{
		{"المكتب الوطني للكهرباء والماء الصالح للشرب", ScriptArabic, "ar", true},
		{"ONEE", ScriptAcronym, "fr", true},
		{"ONEP", ScriptAcronym, "fr", false},
		{"ONE", ScriptAcronym, "fr", false},
	})

	// 4. Maroc Telecom (IAM)
	mIAM := &MasterMerchant{
		ID:                 uuid.MustParse("00000000-0000-0000-0002-000000000001"),
		NormalizedName:     "Maroc Telecom (Itissalat Al-Maghrib S.A.)",
		LegalName:          "Itissalat Al-Maghrib S.A.",
		CountryCode:        "MA",
		MerchantCategory:   "TELECOMMUNICATIONS",
		ICE:                "000054238000045",
		IdentifiantFiscal:  "01004523",
		RegistreCommerce:   "48920_RABAT",
		CNSSNumber:         "1203948",
		PrimaryDomain:      "iam.ma",
		DefaultPCGMAccount: "614510",
		DefaultTVARate:     0.20,
		DefaultTVAAccount:  "345510",
		IsTVADeductible:    true,
	}
	addEntry(mIAM, []struct {
		variant    string
		scriptType ScriptType
		lang       string
		primary    bool
	}{
		{"اتصالات المغرب", ScriptArabic, "ar", true},
		{"ITISSALAT AL-MAGHRIB", ScriptArabicTransliterated, "ar", false},
		{"ITISSALAT", ScriptArabicTransliterated, "ar", false},
		{"IAM", ScriptAcronym, "fr", true},
		{"MAROC TELECOM", ScriptFrenchLegal, "fr", true},
		{"MAROC T", ScriptBankAbbreviation, "fr", false},
		{"M TELECOM", ScriptBankAbbreviation, "fr", false},
		{"MT", ScriptBankAbbreviation, "fr", false},
	})

	// 5. Orange Maroc
	mOrange := &MasterMerchant{
		ID:                 uuid.MustParse("00000000-0000-0000-0002-000000000002"),
		NormalizedName:     "Orange Maroc (Médi Telecom S.A.)",
		LegalName:          "Médi Telecom S.A.",
		CountryCode:        "MA",
		MerchantCategory:   "TELECOMMUNICATIONS",
		ICE:                "000089123000078",
		PrimaryDomain:      "orange.ma",
		DefaultPCGMAccount: "614510",
		DefaultTVARate:     0.20,
		DefaultTVAAccount:  "345510",
		IsTVADeductible:    true,
	}
	addEntry(mOrange, []struct {
		variant    string
		scriptType ScriptType
		lang       string
		primary    bool
	}{
		{"أورنج المغرب", ScriptArabic, "ar", true},
		{"ORANGE MAROC", ScriptFrenchLegal, "fr", true},
		{"ORANGE", ScriptFrenchLegal, "fr", false},
		{"MEDI TELECOM", ScriptFrenchLegal, "fr", false},
		{"MEDITEL", ScriptBankAbbreviation, "fr", false},
	})

	// 6. Barid Al-Maghrib
	mBAM := &MasterMerchant{
		ID:                 uuid.MustParse("00000000-0000-0000-0002-000000000004"),
		NormalizedName:     "Barid Al-Maghrib (Poste Maroc)",
		LegalName:          "Barid Al-Maghrib",
		CountryCode:        "MA",
		MerchantCategory:   "POSTAL_AND_LOGISTICS",
		ICE:                "001500000000077",
		PrimaryDomain:      "poste.ma",
		DefaultPCGMAccount: "614510",
		DefaultTVARate:     0.20,
		DefaultTVAAccount:  "345510",
		IsTVADeductible:    true,
	}
	addEntry(mBAM, []struct {
		variant    string
		scriptType ScriptType
		lang       string
		primary    bool
	}{
		{"بريد المغرب", ScriptArabic, "ar", true},
		{"BARID AL MAGHRIB", ScriptArabicTransliterated, "ar", false},
		{"POSTE MAROC", ScriptFrenchLegal, "fr", true},
		{"BAM", ScriptAcronym, "fr", false},
		{"AMANA EXPRESS", ScriptBankAbbreviation, "fr", false},
	})

	// 7. Afriquia Fuel
	mAfriquia := &MasterMerchant{
		ID:                 uuid.MustParse("00000000-0000-0000-0003-000000000002"),
		NormalizedName:     "Afriquia SMDC (Akwa Group)",
		LegalName:          "Société Marocaine de Distribution de Carburants (Afriquia)",
		CountryCode:        "MA",
		MerchantCategory:   "FUEL_STATION",
		ICE:                "000034567000022",
		PrimaryDomain:      "afriquia.ma",
		DefaultPCGMAccount: "612540",
		DefaultTVARate:     0.14,
		DefaultTVAAccount:  "345510",
		IsTVADeductible:    true,
	}
	addEntry(mAfriquia, []struct {
		variant    string
		scriptType ScriptType
		lang       string
		primary    bool
	}{
		{"أفريقيا", ScriptArabic, "ar", true},
		{"AFRIQUIA", ScriptFrenchLegal, "fr", true},
		{"AFRIQUIA SMDC", ScriptFrenchLegal, "fr", false},
		{"AFRIQUIA GAZ", ScriptFrenchLegal, "fr", false},
		{"AKWA GROUP", ScriptFrenchLegal, "fr", false},
	})

	// 8. TotalEnergies
	mTotal := &MasterMerchant{
		ID:                 uuid.MustParse("00000000-0000-0000-0003-000000000001"),
		NormalizedName:     "TotalEnergies Marketing Maroc S.A.",
		LegalName:          "TotalEnergies Marketing Maroc S.A.",
		CountryCode:        "MA",
		MerchantCategory:   "FUEL_STATION",
		ICE:                "000023456000011",
		PrimaryDomain:      "totalenergies.ma",
		DefaultPCGMAccount: "612540",
		DefaultTVARate:     0.14,
		DefaultTVAAccount:  "345510",
		IsTVADeductible:    true,
	}
	addEntry(mTotal, []struct {
		variant    string
		scriptType ScriptType
		lang       string
		primary    bool
	}{
		{"طوطال إنرجيز", ScriptArabic, "ar", true},
		{"TOTALENERGIES", ScriptFrenchLegal, "fr", true},
		{"TOTAL MAROC", ScriptFrenchLegal, "fr", false},
		{"TOTAL STATION", ScriptBankAbbreviation, "fr", false},
		{"TOTAL", ScriptFrenchLegal, "fr", false},
	})

	// 9. Attijariwafa Bank
	mATW := &MasterMerchant{
		ID:                 uuid.MustParse("00000000-0000-0000-0004-000000000001"),
		NormalizedName:     "Attijariwafa Bank S.A.",
		LegalName:          "Attijariwafa Bank S.A.",
		CountryCode:        "MA",
		MerchantCategory:   "BANK_FINANCIAL",
		ICE:                "000011111000001",
		PrimaryDomain:      "attijariwafabank.com",
		DefaultPCGMAccount: "614700",
		DefaultTVARate:     0.10,
		DefaultTVAAccount:  "345520",
		IsTVADeductible:    true,
	}
	addEntry(mATW, []struct {
		variant    string
		scriptType ScriptType
		lang       string
		primary    bool
	}{
		{"التجاري وفا بنك", ScriptArabic, "ar", true},
		{"ATTIJARIWAFA BANK", ScriptFrenchLegal, "fr", true},
		{"ATW", ScriptAcronym, "fr", false},
		{"ATTIJARI", ScriptBankAbbreviation, "fr", false},
		{"WAFACASH", ScriptBankAbbreviation, "fr", false},
	})

	// 10. Banque Centrale Populaire (BCP)
	mBCP := &MasterMerchant{
		ID:                 uuid.MustParse("00000000-0000-0000-0004-000000000002"),
		NormalizedName:     "Banque Centrale Populaire (BCP)",
		LegalName:          "Banque Centrale Populaire",
		CountryCode:        "MA",
		MerchantCategory:   "BANK_FINANCIAL",
		ICE:                "000022222000002",
		PrimaryDomain:      "groupebcp.com",
		DefaultPCGMAccount: "614700",
		DefaultTVARate:     0.10,
		DefaultTVAAccount:  "345520",
		IsTVADeductible:    true,
	}
	addEntry(mBCP, []struct {
		variant    string
		scriptType ScriptType
		lang       string
		primary    bool
	}{
		{"البنك الشعبي", ScriptArabic, "ar", true},
		{"BANQUE CENTRALE POPULAIRE", ScriptFrenchLegal, "fr", true},
		{"BCP", ScriptAcronym, "fr", true},
		{"BANQUE POPULAIRE", ScriptFrenchLegal, "fr", false},
		{"CHAABI BANK", ScriptBankAbbreviation, "fr", false},
	})

	// 11. Foreign Non-Resident: AWS
	mAWS := &MasterMerchant{
		ID:                 uuid.MustParse("00000000-0000-0000-0005-000000000001"),
		NormalizedName:     "Amazon Web Services EMEA SARL",
		LegalName:          "Amazon Web Services EMEA SARL",
		CountryCode:        "LU",
		MerchantCategory:   "FOREIGN_SAAS_CLOUD",
		PrimaryDomain:      "aws.amazon.com",
		DefaultPCGMAccount: "613670",
		DefaultTVARate:     0.20,
		DefaultTVAAccount:  "345510",
		IsTVADeductible:    true,
		IsForeignService:   true,
		RASApplicable:      true,
		RASRate:            0.10,
	}
	addEntry(mAWS, []struct {
		variant    string
		scriptType ScriptType
		lang       string
		primary    bool
	}{
		{"AWS", ScriptAcronym, "en", true},
		{"AMAZON WEB SERVICES", ScriptFrenchLegal, "en", true},
		{"AWS EMEA SARL", ScriptFrenchLegal, "fr", false},
		{"AWS CLOUD", ScriptBankAbbreviation, "en", false},
	})

	// 12. Foreign Non-Resident: Google Cloud
	mGoogle := &MasterMerchant{
		ID:                 uuid.MustParse("00000000-0000-0000-0005-000000000002"),
		NormalizedName:     "Google Cloud EMEA Limited",
		LegalName:          "Google Cloud EMEA Limited",
		CountryCode:        "IE",
		MerchantCategory:   "FOREIGN_SAAS_CLOUD",
		PrimaryDomain:      "cloud.google.com",
		DefaultPCGMAccount: "613670",
		DefaultTVARate:     0.20,
		DefaultTVAAccount:  "345510",
		IsTVADeductible:    true,
		IsForeignService:   true,
		RASApplicable:      true,
		RASRate:            0.10,
	}
	addEntry(mGoogle, []struct {
		variant    string
		scriptType ScriptType
		lang       string
		primary    bool
	}{
		{"GOOGLE CLOUD", ScriptFrenchLegal, "en", true},
		{"GOOGLE WORKSPACE", ScriptFrenchLegal, "en", false},
		{"GOOGLE IRELAND", ScriptBankAbbreviation, "en", false},
		{"GSUITE", ScriptBankAbbreviation, "en", false},
		{"GOOGLE", ScriptFrenchLegal, "en", false},
	})

	// 13. Foreign Non-Resident: Stripe
	mStripe := &MasterMerchant{
		ID:                 uuid.MustParse("00000000-0000-0000-0005-000000000004"),
		NormalizedName:     "Stripe Technology Europe Limited",
		LegalName:          "Stripe Technology Europe Limited",
		CountryCode:        "IE",
		MerchantCategory:   "FOREIGN_FINTECH",
		PrimaryDomain:      "stripe.com",
		DefaultPCGMAccount: "614700",
		DefaultTVARate:     0.10,
		DefaultTVAAccount:  "345520",
		IsTVADeductible:    true,
		IsForeignService:   true,
		RASApplicable:      true,
		RASRate:            0.10,
	}
	addEntry(mStripe, []struct {
		variant    string
		scriptType ScriptType
		lang       string
		primary    bool
	}{
		{"STRIPE", ScriptFrenchLegal, "en", true},
		{"STRIPE PAYMENTS", ScriptBankAbbreviation, "en", false},
		{"STRIPE EUROPE", ScriptBankAbbreviation, "en", false},
	})

	// 14. Foreign Non-Resident: OpenAI
	mOpenAI := &MasterMerchant{
		ID:                 uuid.MustParse("00000000-0000-0000-0005-000000000006"),
		NormalizedName:     "OpenAI LLC",
		LegalName:          "OpenAI LLC",
		CountryCode:        "US",
		MerchantCategory:   "FOREIGN_SAAS_AI",
		PrimaryDomain:      "openai.com",
		DefaultPCGMAccount: "613670",
		DefaultTVARate:     0.20,
		DefaultTVAAccount:  "345510",
		IsTVADeductible:    true,
		IsForeignService:   true,
		RASApplicable:      true,
		RASRate:            0.10,
	}
	addEntry(mOpenAI, []struct {
		variant    string
		scriptType ScriptType
		lang       string
		primary    bool
	}{
		{"OPENAI", ScriptFrenchLegal, "en", true},
		{"OPENAI API", ScriptBankAbbreviation, "en", false},
		{"CHATGPT", ScriptBankAbbreviation, "en", false},
	})

	// 15. Foreign Non-Resident: Microsoft Azure
	mMSFT := &MasterMerchant{
		ID:                 uuid.MustParse("00000000-0000-0000-0005-000000000003"),
		NormalizedName:     "Microsoft Ireland Operations Limited",
		LegalName:          "Microsoft Ireland Operations Limited",
		CountryCode:        "IE",
		MerchantCategory:   "FOREIGN_SAAS_CLOUD",
		PrimaryDomain:      "azure.microsoft.com",
		DefaultPCGMAccount: "613670",
		DefaultTVARate:     0.20,
		DefaultTVAAccount:  "345510",
		IsTVADeductible:    true,
		IsForeignService:   true,
		RASApplicable:      true,
		RASRate:            0.10,
	}
	addEntry(mMSFT, []struct {
		variant    string
		scriptType ScriptType
		lang       string
		primary    bool
	}{
		{"MICROSOFT AZURE", ScriptFrenchLegal, "en", true},
		{"MSFT AZURE", ScriptBankAbbreviation, "en", false},
		{"MICROSOFT 365", ScriptBankAbbreviation, "en", false},
		{"OFFICE 365", ScriptBankAbbreviation, "en", false},
		{"MICROSOFT", ScriptFrenchLegal, "en", false},
	})

	// 16. Foreign Non-Resident: GitHub
	mGitHub := &MasterMerchant{
		ID:                 uuid.MustParse("00000000-0000-0000-0005-000000000005"),
		NormalizedName:     "GitHub, Inc.",
		LegalName:          "GitHub, Inc.",
		CountryCode:        "US",
		MerchantCategory:   "FOREIGN_SAAS_DEV",
		PrimaryDomain:      "github.com",
		DefaultPCGMAccount: "613670",
		DefaultTVARate:     0.20,
		DefaultTVAAccount:  "345510",
		IsTVADeductible:    true,
		IsForeignService:   true,
		RASApplicable:      true,
		RASRate:            0.10,
	}
	addEntry(mGitHub, []struct {
		variant    string
		scriptType ScriptType
		lang       string
		primary    bool
	}{
		{"GITHUB", ScriptFrenchLegal, "en", true},
		{"GITHUB INC", ScriptFrenchLegal, "en", false},
	})
}
