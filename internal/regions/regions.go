// Package regions serves the mainland China province/city/area cascade used by
// the registration form. The data is a vendored copy of
// modood/Administrative-divisions-of-China dist/pca-code.json (2023 statistics
// division, WTFPL), so the page needs no runtime network dependency.
//
// Values are composed in the legacy free-text style used by the v1 form:
//   - province+city:  "广东深圳" (municipalities collapse to "北京")
//   - city+area:      "深圳南山"
//
// This keeps new registrations comparable with migrated rows such as "广东深圳".
package regions

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

//go:embed pca-code.json
var rawPCA []byte

type rawArea struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type rawCity struct {
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	Children []rawArea `json:"children"`
}

type rawProvince struct {
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	Children []rawCity `json:"children"`
}

// Area is one county-level division.
type Area struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// City is one prefecture-level division. Areas may be empty for divisions that
// have no county-level children in the source data (for example 济源).
type City struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Value string `json:"value"`
	Areas []Area `json:"areas,omitempty"`
}

// Province is one province-level division.
type Province struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Cities []City `json:"cities"`
}

var (
	data            []Province
	jsonData        []byte
	cityValue       = map[string]string{}
	cityProv        = map[string]string{}
	areaValue       = map[string]string{}
	areaCity        = map[string]string{}
	cityHasArea     = map[string]bool{}
	cityByValue     = map[string]cityRef{}
	cityAmbiguous   = map[string]bool{}
	areaByValue     = map[string]areaRef{}
	areaAmbiguous   = map[string]bool{}
	cityValueNoArea = map[string]cityRef{}
	cityAlias       = map[string]string{}
	areaAlias       = map[string]string{}
	noAreaAlias     = map[string]string{}
	jsonETag        string
)

type cityRef struct {
	Province string
	City     string
}

type areaRef struct {
	Province string
	City     string
	Area     string
}

func init() {
	var raw []rawProvince
	if err := json.Unmarshal(rawPCA, &raw); err != nil {
		panic("regions: parse pca-code.json: " + err.Error())
	}
	for _, p := range raw {
		data = append(data, buildProvince(p))
	}
	data = append(data, supplementalProvinces()...)
	for _, p := range data {
		for _, c := range p.Cities {
			cityValue[c.Code] = c.Value
			cityProv[c.Code] = p.Code
			ref := cityRef{Province: p.Code, City: c.Code}
			if _, exists := cityByValue[c.Value]; exists {
				delete(cityByValue, c.Value) // same display value, different codes
				cityAmbiguous[c.Value] = true
			} else if !cityAmbiguous[c.Value] {
				cityByValue[c.Value] = ref
			}
			if len(c.Areas) > 0 {
				cityHasArea[c.Code] = true
			} else {
				cityValueNoArea[c.Value] = cityRef{Province: p.Code, City: c.Code}
			}
			for _, a := range c.Areas {
				areaValue[a.Code] = a.Value
				areaCity[a.Code] = c.Code
				ref := areaRef{Province: p.Code, City: c.Code, Area: a.Code}
				if _, exists := areaByValue[a.Value]; exists {
					delete(areaByValue, a.Value) // canonical duplicate: cannot preselect a code
					areaAmbiguous[a.Value] = true
				} else if !areaAmbiguous[a.Value] {
					areaByValue[a.Value] = ref
				}
			}
		}
	}
	ambiguousAlias := map[string]bool{}
	addAlias := func(dst map[string]string, alias, value string) {
		if alias == "" || alias == value || ambiguousAlias[alias] {
			return
		}
		if previous, ok := dst[alias]; ok && previous != value {
			// The same short name may already exist in another namespace
			// (district vs no-area city); remove it everywhere.
			delete(cityAlias, alias)
			delete(noAreaAlias, alias)
			delete(areaAlias, alias)
			ambiguousAlias[alias] = true
			return
		}
		dst[alias] = value
	}
	for _, p := range data {
		for _, c := range p.Cities {
			if !cityAmbiguous[c.Value] {
				if short := shortCity(c.Name); short != "" {
					addAlias(cityAlias, short, c.Value)
				}
				addAlias(cityAlias, cityStem(c.Name), c.Value)
				addAlias(cityAlias, c.Name, c.Value)
			}
			if len(c.Areas) == 0 && !cityAmbiguous[c.Value] {
				if short := shortCity(c.Name); short != "" {
					addAlias(noAreaAlias, short, c.Value)
				}
				addAlias(noAreaAlias, cityStem(c.Name), c.Value)
				addAlias(noAreaAlias, c.Name, c.Value)
			}
			for _, a := range c.Areas {
				for _, suffix := range []string{"区", "县", "市", "旗"} {
					addAlias(areaAlias, a.Value+suffix, a.Value)
				}
				addAlias(areaAlias, a.Name, a.Value)
				addAlias(areaAlias, shortArea(a.Name), a.Value)
			}
		}
	}
	var err error
	jsonData, err = json.Marshal(struct {
		Provinces []Province `json:"provinces"`
	}{Provinces: data})
	if err != nil {
		panic("regions: marshal: " + err.Error())
	}
	sum := sha256.Sum256(jsonData)
	jsonETag = hex.EncodeToString(sum[:])
}

var provinceShortNames = map[string]string{
	"11": "北京", "12": "天津", "13": "河北", "14": "山西", "15": "内蒙古",
	"21": "辽宁", "22": "吉林", "23": "黑龙江", "31": "上海", "32": "江苏",
	"33": "浙江", "34": "安徽", "35": "福建", "36": "江西", "37": "山东",
	"41": "河南", "42": "湖北", "43": "湖南", "44": "广东", "45": "广西",
	"46": "海南", "50": "重庆", "51": "四川", "52": "贵州", "53": "云南",
	"54": "西藏", "61": "陕西", "62": "甘肃", "63": "青海", "64": "宁夏",
	"65": "新疆", "71": "台湾", "81": "香港", "82": "澳门",
}

var municipalities = map[string]bool{"11": true, "12": true, "31": true, "50": true}

func provinceShort(code, name string) string {
	if short := provinceShortNames[code]; short != "" {
		return short
	}
	return trimSuffix(name, "省", "市", "自治区", "特别行政区")
}

// buildProvince turns one raw province into the client shape. Municipalities
// merge their 市辖区/县 pseudo-cities into a single city so 北京/重庆 have one
// stable city entry with all districts as areas.
func buildProvince(p rawProvince) Province {
	short := provinceShort(p.Code, p.Name)
	out := Province{Code: p.Code, Name: p.Name}
	if municipalities[p.Code] {
		city := City{Code: p.Code + "01", Name: p.Name, Value: short}
		for _, rc := range p.Children {
			for _, ra := range rc.Children {
				area := Area{Code: ra.Code, Name: ra.Name, Value: short + shortArea(ra.Name)}
				city.Areas = append(city.Areas, area)
			}
		}
		out.Cities = append(out.Cities, city)
		return out
	}
	for _, rc := range p.Children {
		if isDirectCountyCity(rc.Name) {
			// 省直辖县级行政区划：promote each county to a city-level choice so
			// 济源/仙桃/石河子 etc. remain selectable.
			for _, ra := range rc.Children {
				out.Cities = append(out.Cities, City{
					Code: ra.Code, Name: ra.Name, Value: short + shortArea(ra.Name),
				})
			}
			continue
		}
		cityShort := shortCity(rc.Name)
		cityVal := short
		if cityShort != "" && cityShort != short {
			cityVal = short + cityShort
		}
		city := City{Code: rc.Code, Name: rc.Name, Value: cityVal}
		for _, ra := range rc.Children {
			areaShort := shortArea(ra.Name)
			prefix := cityShort
			if prefix == "" {
				prefix = short
			}
			city.Areas = append(city.Areas, Area{
				Code: ra.Code, Name: ra.Name, Value: prefix + areaShort,
			})
		}
		out.Cities = append(out.Cities, city)
	}
	return out
}

func isDirectCountyCity(name string) bool {
	return strings.Contains(name, "直辖县级行政区划")
}

func trimSuffix(value string, suffixes ...string) string {
	for _, suffix := range suffixes {
		if strings.HasSuffix(value, suffix) {
			return strings.TrimSuffix(value, suffix)
		}
	}
	return value
}

var ethnicGroups = []string{
	"柯尔克孜", "乌孜别克", "鄂伦春", "鄂温克", "达斡尔", "塔吉克", "塔塔尔",
	"俄罗斯", "哈萨克", "维吾尔", "朝鲜", "土家", "哈尼", "傈僳", "景颇",
	"布依", "仡佬", "仫佬", "毛南", "撒拉", "东乡", "保安", "裕固", "纳西",
	"拉祜", "德昂", "阿昌", "普米", "独龙", "基诺", "门巴", "珞巴", "锡伯",
	"蒙古", "藏", "回", "苗", "彝", "壮", "满", "侗", "瑶", "白", "傣",
	"黎", "佤", "畲", "水", "土", "羌", "怒", "京", "高山",
}

func shortCity(name string) string {
	if name == "市辖区" || name == "县" {
		return ""
	}
	autonomous := strings.HasSuffix(name, "自治州") || strings.HasSuffix(name, "自治县")
	name = trimSuffix(name, "自治州", "自治县", "地区", "盟", "市")
	if autonomous {
		for changed := true; changed; {
			changed = false
			for _, ethnic := range ethnicGroups {
				for _, suffix := range []string{ethnic + "族", ethnic} {
					if strings.HasSuffix(name, suffix) {
						name = strings.TrimSuffix(name, suffix)
						changed = true
					}
				}
			}
		}
	}
	return name
}

// cityStem is the shortest unambiguous name used to detect county/city
// collisions such as 新竹市 vs 新竹县.
func cityStem(name string) string {
	name = trimSuffix(name, "自治州", "自治县", "地区", "盟", "市", "县", "旗")
	autonomous := true
	for changed := autonomous; changed; {
		changed = false
		for _, ethnic := range ethnicGroups {
			for _, suffix := range []string{ethnic + "族", ethnic} {
				if strings.HasSuffix(name, suffix) {
					name = strings.TrimSuffix(name, suffix)
					changed = true
				}
			}
		}
	}
	return name
}

func shortArea(name string) string {
	return trimSuffix(name, "自治县", "自治旗", "林区", "特区", "新区", "区", "县", "市", "旗")
}

// supplementalProvinces covers Hong Kong, Macau and Taiwan, which the mainland
// statistics list does not include. Values follow the same legacy style.
func supplementalProvinces() []Province {
	hkDistricts := []string{
		"中西区", "湾仔区", "东区", "南区", "油尖旺区", "深水埗区", "九龙城区",
		"黄大仙区", "观塘区", "葵青区", "荃湾区", "屯门区", "元朗区", "北区",
		"大埔区", "沙田区", "西贡区", "离岛区",
	}
	moParishes := []string{
		"花地玛堂区", "圣安多尼堂区", "大堂区", "望德堂区", "风顺堂区",
		"嘉模堂区", "圣方济各堂区", "路氹填海区",
	}
	twCities := []string{
		"台北市", "新北市", "桃园市", "台中市", "台南市", "高雄市", "基隆市",
		"新竹市", "嘉义市", "新竹县", "苗栗县", "彰化县", "南投县", "云林县",
		"嘉义县", "屏东县", "宜兰县", "花莲县", "台东县", "澎湖县",
	}
	return []Province{
		{Code: "81", Name: "香港特别行政区", Cities: []City{{
			Code: "8101", Name: "香港", Value: "香港",
			Areas: makeAreasHKA(hkDistricts),
		}}},
		{Code: "82", Name: "澳门特别行政区", Cities: []City{{
			Code: "8201", Name: "澳门", Value: "澳门",
			Areas: makeAreasMO(moParishes),
		}}},
		{Code: "71", Name: "台湾省", Cities: func() []City {
			out := make([]City, 0, len(twCities))
			for i, name := range twCities {
				short := shortCity(name)
				out = append(out, City{
					Code: "71" + pad2(i+1), Name: name, Value: "台湾" + short,
				})
			}
			return out
		}()},
	}
}

func makeAreasHKA(names []string) []Area {
	out := make([]Area, 0, len(names))
	for i, name := range names {
		out = append(out, Area{Code: "8101" + pad2(i+1), Name: name, Value: "香港" + shortArea(name)})
	}
	return out
}

func makeAreasMO(names []string) []Area {
	out := make([]Area, 0, len(names))
	for i, name := range names {
		out = append(out, Area{Code: "8201" + pad2(i+1), Name: name, Value: "澳门" + shortArea(name)})
	}
	return out
}

func pad2(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// JSON returns the client payload. It is built once at startup.
func JSON() []byte { return jsonData }

// ETag returns a strong validator for JSON.
func ETag() string { return jsonETag }

// LookupProvinceCity validates a province/city pair and returns its legacy
// display value ("广东深圳").
func LookupProvinceCity(provinceCode, cityCode string) (string, bool) {
	if cityProv[cityCode] != provinceCode {
		return "", false
	}
	value, ok := cityValue[cityCode]
	return value, ok
}

// LookupCityArea validates a city/area pair and returns its legacy display
// value ("深圳南山"). If the city has no areas, the city value is returned.
func LookupCityArea(provinceCode, cityCode, areaCode string) (string, bool) {
	if cityProv[cityCode] != provinceCode {
		return "", false
	}
	if areaCode == "" {
		if cityHasArea[cityCode] {
			return "", false
		}
		value, ok := cityValue[cityCode]
		return value, ok
	}
	if areaCity[areaCode] != cityCode {
		return "", false
	}
	value, ok := areaValue[areaCode]
	return value, ok
}

// NormalizeCityValue maps a province-city value (canonical "广东深圳" or the
// legacy short form "深圳") back to the canonical value. Values are compared
// without spaces and hyphens so old "陕西-宝鸡" rows remain editable.
func NormalizeCityValue(value string) (string, bool) {
	value = cleanRegionValue(value)
	if value == "" {
		return "", false
	}
	if _, ok := cityByValue[value]; ok {
		return value, true
	}
	if cityAmbiguous[value] {
		return value, true
	}
	if canonical, ok := cityAlias[value]; ok && !cityAmbiguous[canonical] {
		return canonical, true
	}
	return "", false
}

// LookupCityValue returns the province/city codes for a canonical or legacy
// city value. It is used to preselect the profile edit cascade.
func LookupCityValue(value string) (provinceCode, cityCode string, ok bool) {
	canonical, ok := NormalizeCityValue(value)
	if !ok || cityAmbiguous[canonical] {
		return "", "", false
	}
	ref := cityByValue[canonical]
	return ref.Province, ref.City, true
}

// NormalizeAreaValue maps a city-area value (canonical "深圳南山" or the legacy
// "深圳南山区") back to the canonical value.
func NormalizeAreaValue(value string) (string, bool) {
	value = cleanRegionValue(value)
	if value == "" {
		return "", false
	}
	if _, ok := areaByValue[value]; ok {
		return value, true
	}
	if areaAmbiguous[value] {
		return value, true // a real dataset value, but the code is ambiguous
	}
	if canonical, ok := areaAlias[value]; ok {
		// A short name can be both a district (常州新北) and a no-area city
		// (台湾新北); never guess. The user must re-select from the cascade.
		if _, collision := noAreaAlias[value]; collision {
			return "", false
		}
		return canonical, true
	}
	if canonical, ok := noAreaAlias[value]; ok {
		return canonical, true
	}
	if _, ok := cityValueNoArea[value]; ok {
		return value, true
	}
	return "", false
}

// LookupAreaValue returns the province/city/area codes for a canonical or
// legacy area value.
func LookupAreaValue(value string) (provinceCode, cityCode, areaCode string, ok bool) {
	canonical, ok := NormalizeAreaValue(value)
	if !ok || areaAmbiguous[canonical] {
		return "", "", "", false
	}
	if ref, ok := areaByValue[canonical]; ok {
		return ref.Province, ref.City, ref.Area, true
	}
	if ref, ok := cityValueNoArea[canonical]; ok {
		if cityAmbiguous[canonical] {
			return "", "", "", false
		}
		return ref.Province, ref.City, "", true
	}
	return "", "", "", false
}

func cleanRegionValue(value string) string {
	replacer := strings.NewReplacer(" ", "", "\t", "", "-", "", "－", "")
	return replacer.Replace(strings.TrimSpace(value))
}

// ValidateManual accepts a manually typed region fallback (港澳台/海外/旧地址).
func ValidateManual(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if n := len([]rune(value)); n < 2 || n > 60 {
		return "", false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return value, true
}

// sortedCodes is only used by tests to keep deterministic iteration.
func sortedCodes() []string {
	codes := make([]string, 0, len(cityValue))
	for code := range cityValue {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}
