package fignode

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"encoding/json"
	"strings"
)

type VendorTaxonomy struct {
	Industry          string `json:"industry"`
	IndustryIcon      string `json:"industry_icon"`
	VendorDescription string `json:"vendor_description"`
	VendorUrl         string `json:"vendor_url"`
}

type CompanyTaxonomy struct {
	Industry      string `json:"industry"`
	IndustryIcon  string `json:"industry_icon"`
	BusinessModel string `json:"business_model"`
	MindsetHint   string `json:"mindset_hint"`
}

func EnsureVendorContext(ctx context.Context, db *database.Queries, rt *agent.Runtime, vendor database.ShadowErpVendor) (VendorTaxonomy, error) {
	var result VendorTaxonomy
	if vendor.Industry.Valid && vendor.Industry.String != "" {
		result.Industry = vendor.Industry.String
		result.IndustryIcon = vendor.IndustryIcon.String
		result.VendorDescription = vendor.VendorDescription.String
		result.VendorUrl = vendor.VendorUrl.String
		return result, nil
	}

	if rt == nil {
		return result, nil
	}

	sysPrompt := "You are a categorical metadata generator. Generate a precise JSON taxonomy for the following vendor. Include a 1 character emoji for industry_icon, a short vendor_description, and their likely vendor_url. Output ONLY valid JSON."
	usrPrompt := "Vendor Name: " + vendor.DisplayName

	resp, err := rt.Exec(ctx, usrPrompt, sysPrompt)
	if err != nil {
		return result, err
	}

	cleanStr := strings.TrimSpace(resp)
	cleanStr = strings.TrimPrefix(cleanStr, "```json")
	cleanStr = strings.TrimPrefix(cleanStr, "```")
	cleanStr = strings.TrimSuffix(cleanStr, "```")

	if err := json.Unmarshal([]byte(cleanStr), &result); err != nil {
		return result, err
	}

	_ = db.UpdateVendorTaxonomy(ctx, database.UpdateVendorTaxonomyParams{
		RealmID:           vendor.RealmID,
		ErpID:             vendor.ErpID,
		Industry:          pgtype.Text{String: result.Industry, Valid: result.Industry != ""},
		IndustryIcon:      pgtype.Text{String: result.IndustryIcon, Valid: result.IndustryIcon != ""},
		VendorDescription: pgtype.Text{String: result.VendorDescription, Valid: result.VendorDescription != ""},
		VendorUrl:         pgtype.Text{String: result.VendorUrl, Valid: result.VendorUrl != ""},
	})
	return result, nil
}

func EnsureCompanyContext(ctx context.Context, db *database.Queries, rt *agent.Runtime, comp database.ShadowErpCompanyInfo) (CompanyTaxonomy, error) {
	var result CompanyTaxonomy
	if comp.Industry.Valid && comp.Industry.String != "" {
		result.Industry = comp.Industry.String
		result.IndustryIcon = comp.IndustryIcon.String
		result.BusinessModel = comp.BusinessModel.String
		result.MindsetHint = comp.MindsetHint.String
		return result, nil
	}

	if rt == nil {
		return result, nil
	}

	sysPrompt := "You are a professional accountant generating gamification metadata. Provide a descriptive taxonomy matching this accounting entity's business profile. industry_icon is a 1 character emoji. mindset_hint is a short 8-word sentence on what strict CPA compliance rules this type of firm requires. Output ONLY valid JSON."
	usrPrompt := "Firm Name: " + comp.CompanyName

	resp, err := rt.Exec(ctx, usrPrompt, sysPrompt)
	if err != nil {
		return result, err
	}

	cleanStr := strings.TrimSpace(resp)
	cleanStr = strings.TrimPrefix(cleanStr, "```json")
	cleanStr = strings.TrimPrefix(cleanStr, "```")
	cleanStr = strings.TrimSuffix(cleanStr, "```")

	if err := json.Unmarshal([]byte(cleanStr), &result); err != nil {
		return result, err
	}

	_ = db.UpdateCompanyTaxonomy(ctx, database.UpdateCompanyTaxonomyParams{
		RealmID:       comp.RealmID,
		Industry:      pgtype.Text{String: result.Industry, Valid: result.Industry != ""},
		IndustryIcon:  pgtype.Text{String: result.IndustryIcon, Valid: result.IndustryIcon != ""},
		BusinessModel: pgtype.Text{String: result.BusinessModel, Valid: result.BusinessModel != ""},
		MindsetHint:   pgtype.Text{String: result.MindsetHint, Valid: result.MindsetHint != ""},
	})
	return result, nil
}

type CustomerTaxonomy struct {
	Industry            string `json:"industry"`
	IndustryIcon        string `json:"industry_icon"`
	CustomerDescription string `json:"customer_description"`
	CustomerUrl         string `json:"customer_url"`
}

func EnsureCustomerContext(ctx context.Context, db *database.Queries, rt *agent.Runtime, customer database.ShadowErpCustomer) (CustomerTaxonomy, error) {
	var result CustomerTaxonomy
	if customer.Industry.Valid && customer.Industry.String != "" {
		result.Industry = customer.Industry.String
		result.IndustryIcon = customer.IndustryIcon.String
		result.CustomerDescription = customer.CustomerDescription.String
		result.CustomerUrl = customer.CustomerUrl.String
		return result, nil
	}

	if rt == nil {
		return result, nil
	}

	sysPrompt := "You are a categorical metadata generator. Generate a precise JSON taxonomy for the following customer. Include a 1 character emoji for industry_icon, a short customer_description, and their likely customer_url. Output ONLY valid JSON."
	usrPrompt := "Customer Name: " + customer.DisplayName

	resp, err := rt.Exec(ctx, usrPrompt, sysPrompt)
	if err != nil {
		return result, err
	}

	cleanStr := strings.TrimSpace(resp)
	cleanStr = strings.TrimPrefix(cleanStr, "```json")
	cleanStr = strings.TrimPrefix(cleanStr, "```")
	cleanStr = strings.TrimSuffix(cleanStr, "```")

	if err := json.Unmarshal([]byte(cleanStr), &result); err != nil {
		return result, err
	}

	_ = db.UpdateCustomerTaxonomy(ctx, database.UpdateCustomerTaxonomyParams{
		RealmID:             customer.RealmID,
		ErpID:               customer.ErpID,
		Industry:            pgtype.Text{String: result.Industry, Valid: result.Industry != ""},
		IndustryIcon:        pgtype.Text{String: result.IndustryIcon, Valid: result.IndustryIcon != ""},
		CustomerDescription: pgtype.Text{String: result.CustomerDescription, Valid: result.CustomerDescription != ""},
		CustomerUrl:         pgtype.Text{String: result.CustomerUrl, Valid: result.CustomerUrl != ""},
	})
	return result, nil
}
