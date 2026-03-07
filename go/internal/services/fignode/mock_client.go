package fignode

import (
	"fmt"
	"math"
)

type MockClient struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Industry    string  `json:"industry"`
	Phone       string  `json:"phone"`
	Cash        float64 `json:"cash"`
	AR          float64 `json:"ar"`
	AP          float64 `json:"ap"`
	MonthlyBurn float64 `json:"monthlyBurn"`
}

var MockClients = []MockClient{
	{
		ID:          "c1",
		Name:        "Apex Roofing Co.",
		Industry:    "Construction",
		Phone:       "+15550001001",
		Cash:        284_500,
		AR:          112_000,
		AP:          67_000,
		MonthlyBurn: 88_000,
	},
	{
		ID:          "c2",
		Name:        "NovaMed Health",
		Industry:    "Healthcare",
		Phone:       "+15550001002",
		Cash:        510_000,
		AR:          340_000,
		AP:          95_000,
		MonthlyBurn: 210_000,
	},
	{
		ID:          "c3",
		Name:        "Bolt Electric LLC",
		Industry:    "Trades",
		Phone:       "+15550001003",
		Cash:        48_200,
		AR:          22_100,
		AP:          31_400,
		MonthlyBurn: 36_000,
	},
	{
		ID:          "c4",
		Name:        "ClearPath Logistics",
		Industry:    "Transportation",
		Phone:       "+15550001004",
		Cash:        1_200_000,
		AR:          450_000,
		AP:          180_000,
		MonthlyBurn: 320_000,
	},
	{
		ID:          "c5",
		Name:        "Brindlewood Bakery",
		Industry:    "Food & Beverage",
		Phone:       "+15550001005",
		Cash:        18_400,
		AR:          4_200,
		AP:          9_800,
		MonthlyBurn: 14_500,
	},
	{
		ID:          "c6",
		Name:        "Tessera Digital",
		Industry:    "Technology",
		Phone:       "+15550001006",
		Cash:        920_000,
		AR:          210_000,
		AP:          55_000,
		MonthlyBurn: 185_000,
	},
}

// Fmt formats a number in K or M
func Fmt(n float64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("$%.2fM", n/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("$%.1fK", n/1_000)
	}
	return fmt.Sprintf("$%.0f", n)
}

// Runway calculates the runway in months
func Runway(client MockClient) float64 {
	trueRunway := (client.Cash + client.AR - client.AP) / client.MonthlyBurn
	return math.Max(trueRunway, 0)
}

// RunwayColor returns the runway color based on months
func RunwayColor(months float64) string {
	if months < 2 {
		return "#EF4444"
	}
	if months < 4 {
		return "#F59E0B"
	}
	return "#22C55E"
}

// RunwayDot returns the runway dot emoji based on months
func RunwayDot(months float64) string {
	if months < 2 {
		return "🔴"
	}
	if months < 4 {
		return "🟡"
	}
	return "🟢"
}
