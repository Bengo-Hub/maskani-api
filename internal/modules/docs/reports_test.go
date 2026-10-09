package docs

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Bengo-Hub/reports"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	analytics "github.com/bengobox/maskani-api/internal/modules/reports"
)

func sampleInsights() *analytics.Insights {
	d := func(f float64) decimal.Decimal { return decimal.NewFromFloat(f) }
	p := func(f float64) *decimal.Decimal { v := decimal.NewFromFloat(f); return &v }
	v := &analytics.Insights{Period: "2026-09"}
	for i := 0; i < 12; i++ {
		m := time.Date(2025, time.Month(10+i), 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
		billed := 1_500_000 + float64(i)*20_000
		v.Months = append(v.Months, analytics.MonthPoint{Period: m, Billed: d(billed), Collected: d(billed * 0.86), CollectionRate: p(86)})
	}
	v.KPIs = analytics.InsightKPIs{
		CollectionRate: analytics.KPI{Value: d(88.4), LastMonth: p(85.1)},
		Billed:         analytics.KPI{Value: d(1_720_000), LastMonth: p(1_700_000)},
		Collected:      analytics.KPI{Value: d(1_520_480), LastMonth: p(1_447_000)},
		Outstanding:    d(640_000), DaysSalesOutstanding: p(11), Units: 240, Occupied: 212, OccupancyPct: p(88.3), OpenWorkOrders: 14,
	}
	v.ForecastBasis.Method = "Instalments due plus three-month average billing times the six-month collection rate."
	for i := 1; i <= 3; i++ {
		v.Forecast = append(v.Forecast, analytics.ForecastRow{Period: fmt.Sprintf("2026-%02d", 9+i), Instalments: d(400_000), Recurring: d(1_480_000), Total: d(1_880_000)})
	}
	v.RevenueMix = []analytics.MixRow{{ChargeCode: "service_charge", Amount: d(4_200_000)}, {ChargeCode: "water", Amount: d(780_000)}, {ChargeCode: "garbage", Amount: d(120_000)}}
	v.Blocks = []analytics.BlockRow{{BlockID: uuid.New(), Name: "Block A", Units: 120, Billed: d(860_000), Owing: d(310_000)}, {BlockID: uuid.New(), Name: "Block B", Units: 120, Billed: d(860_000), Owing: d(330_000)}}
	v.WorkByType = []analytics.WorkCategoryRow{{Category: "plumbing", Opened: 22, Completed: 19, Breached: 2, AvgResolveHours: p(30)}}
	v.Sales = analytics.SalesOutlook{SignedLast6m: 9, MonthlyRate: d(1.5), Available: 18, MonthsToSellOut: p(12), ContractValue: d(98_000_000), ContractCollected: d(61_000_000)}
	return v
}

// TestPerformanceReport renders the performance report; $PERFORMANCE_SAMPLE_PDF writes the PDF.
func TestPerformanceReport(t *testing.T) {
	r := &reports.Report{Title: "Performance", Subtitle: "Month of September 2026", GeneratedAt: time.Now(), Currency: "KES",
		TenantName: "Shaba Village Estate", PrimaryColor: "#8E2C76"}
	performanceReport(r, sampleInsights())
	if r.Cards[0].Sub != "+3.3 points on last month" {
		t.Fatalf("collection rate change = %q", r.Cards[0].Sub)
	}
	for _, f := range []reports.Format{reports.FormatPDF, reports.FormatCSV, reports.FormatXLSX} {
		b, _, err := reports.Generate(r, f)
		if err != nil || len(b) == 0 {
			t.Fatalf("%s: %v", f, err)
		}
		if out := os.Getenv("PERFORMANCE_SAMPLE_PDF"); out != "" && f == reports.FormatPDF {
			_ = os.WriteFile(out, b, 0o644)
		}
	}
}
