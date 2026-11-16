# Cross-station QC and averaging

## Finding

Averaging nearby sensors does not improve accuracy. Consumer-sensor error is
mostly systematic (siting, solar heating, shielding), which does not average
out. Published work uses neighbors for QC and bias adjustment.

## Design

- Within a station: plausibility, temporal consistency, and median across
  redundant sensors.
- Pressure: average is valid after altitude correction.
- Temperature and humidity: marginal. Wind and rain: point measurements, do not
  average.
- Server-side QC across all WxAlerts stations (km scale, not RF range): outlier
  flags and bias estimates.
- Store raw plus a separate derived consensus value. Never overwrite raw.

## References

- CrowdQC+ (Frontiers in Environmental Science, 2021):
  https://www.frontiersin.org/journals/environmental-science/articles/10.3389/fenvs.2021.720747/pdf
- EUMETNET PWS QC study (Met Office, SMHI):
  https://www.eumetnet.eu/wp-content/uploads/2023/06/Crowdsourcing-PWS-Quality-Control-A1.05_MetOffice_Final_2022.pdf

## Where it runs (Nov 16)

- The QC worker runs against the existing PostGIS observation database that
  already holds APRS, METAR and CWOP data. It can compare each PWS against
  nearby stations from all of those sources with a distance query.
- No ownership attestation exists in the product (decided). QC signals such as
  consistently weak RSSI are the technical check on foreign sensors.

## Repo notes (2026-11-16)

- The QC worker is server-side and lives in the private repositories. The only
  device-side part is within-station plausibility (used by discovery and by the
  `n` sample count), which is in this repo.
