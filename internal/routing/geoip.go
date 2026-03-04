package routing

import (
	"io"
	"net"
	"net/http"
	"os"

	"github.com/oschwald/geoip2-golang"
)

// GeoIPMatcher 地理位置和 ASN 匹配器
type GeoIPMatcher struct {
	db    *geoip2.Reader
	asnDB *geoip2.Reader
}

func NewGeoIPMatcher(dbPath string, asnDBPath string) (*GeoIPMatcher, error) {
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		if err := downloadFile(dbPath, "https://github.com/P3TERX/GeoLite2-City-v2ray/raw/gh-pages/GeoLite2-City.mmdb"); err != nil {
			return nil, err
		}
	}
	db, err := geoip2.Open(dbPath)
	if err != nil {
		return nil, err
	}

	var asnDB *geoip2.Reader
	if asnDBPath != "" {
		if _, err := os.Stat(asnDBPath); os.IsNotExist(err) {
			_ = downloadFile(asnDBPath, "https://github.com/P3TERX/GeoLite2-City-v2ray/raw/gh-pages/GeoLite2-ASN.mmdb")
		}
		if _, err := os.Stat(asnDBPath); err == nil {
			asnDB, _ = geoip2.Open(asnDBPath)
		}
	}

	return &GeoIPMatcher{db: db, asnDB: asnDB}, nil
}

func (m *GeoIPMatcher) Match(ip net.IP, countryCode string) bool {
	if m.db == nil {
		return false
	}
	record, err := m.db.Country(ip)
	if err != nil {
		return false
	}
	return record.Country.IsoCode == countryCode
}

func (m *GeoIPMatcher) MatchASN(ip net.IP, asn uint) bool {
	if m.asnDB == nil {
		return false
	}
	record, err := m.asnDB.ASN(ip)
	if err != nil {
		return false
	}
	return record.AutonomousSystemNumber == asn
}

func (m *GeoIPMatcher) Close() error {
	if m.db != nil {
		m.db.Close()
	}
	if m.asnDB != nil {
		m.asnDB.Close()
	}
	return nil
}

func downloadFile(filepath string, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}
