package main

import (
	"testing"

	"github.com/7310C7310C/yixianqian/internal/regions"
)

func TestRepairRowNormalizesLegacyValues(t *testing.T) {
	fix := repairRow("深圳", "汕头", "深圳南山区")
	if fix.City != "广东深圳" || fix.NativePlace != "广东汕头" || fix.ChurchLocation != "深圳南山" {
		t.Fatalf("unexpected repair: %+v", fix)
	}
	if !fix.CityChanged || !fix.NativeChanged || !fix.ChurchChanged {
		t.Fatalf("expected all changed: %+v", fix)
	}
	if fix.CityFallback || fix.NativeFallback || fix.ChurchFallback {
		t.Fatalf("unexpected fallback: %+v", fix)
	}
}

func TestRepairRowFallsBackToCityAndArea(t *testing.T) {
	fix := repairRow("", "", "")
	if fix.City != fallbackCity || fix.NativePlace != fallbackCity {
		t.Fatalf("fallback city/native = %+v", fix)
	}
	if !fix.CityFallback || !fix.NativeFallback || !fix.ChurchFallback {
		t.Fatalf("expected fallback flags: %+v", fix)
	}
	if _, ok := regions.NormalizeAreaValue(fix.ChurchLocation); !ok {
		t.Fatalf("church fallback %q is not valid", fix.ChurchLocation)
	}
}

func TestRepairRowInvalidNativeUsesCity(t *testing.T) {
	fix := repairRow("广东广州", "好", "")
	if fix.NativePlace != "广东广州" || !fix.NativeFallback {
		t.Fatalf("unexpected native repair: %+v", fix)
	}
}

func TestRepairRowKeepsCanonicalValues(t *testing.T) {
	fix := repairRow("广东深圳", "广东汕头", "深圳南山")
	if fix.CityChanged || fix.NativeChanged || fix.ChurchChanged {
		t.Fatalf("canonical values should be unchanged: %+v", fix)
	}
}

func TestRepairRowKeepsCityShortNameSharedWithDistrict(t *testing.T) {
	fix := repairRow("西安", "西安", "")
	if fix.City != "陕西西安" || fix.NativePlace != "陕西西安" {
		t.Fatalf("西安 must not fall back to 北京: %+v", fix)
	}
	if fix.CityFallback || fix.NativeFallback {
		t.Fatalf("unexpected fallback: %+v", fix)
	}
	if _, ok := regions.NormalizeAreaValue(fix.ChurchLocation); !ok {
		t.Fatalf("church fallback %q is not valid", fix.ChurchLocation)
	}
}
