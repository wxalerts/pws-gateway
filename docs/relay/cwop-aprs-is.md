# CWOP ingest status and APRS-IS login rules

Settles issue #2. Research date: 2026-11-17. Where a source is old or only
indirect, that is stated.

## Sources

- CWOP, "APRS Servers to Use", http://www.wxqa.com/servers2use.html (last
  changed 2016-11-14). The authoritative server guidance.
- CWOP FAQ, http://www.wxqa.com/faq.html (page generated 2021-06-06): login
  line, packet format, minimum interval.
- APRS-IS, "Connecting to APRS-IS", https://www.aprs-is.net/Connecting.aspx
  (login syntax, ports, passcode `-1`).
- NOAA MADIS, "CWOP", https://madis.ncep.noaa.gov/madis_cwop.shtml (last
  updated 2021-12-02): how CWOP data reaches MADIS.
- University of Utah MesoWest, https://mesowest.utah.edu/ : "MesoWest Sunset
  Date December 31, 2026". Synoptic Data newsletter, January 2026,
  https://synopticdata.com/newsletters/january-2026/ : MesoWest ends services
  2026-12-31.

## Ingest status after the MesoWest sunset

The MesoWest sunset is the end of a viewer and redistributor at the University
of Utah, announced 2026-01-01 for 2026-12-31. CWOP itself does not flow through
MesoWest: stations send APRS packets to the APRS-IS CWOP servers, findU relays
them every 5 minutes to NOAA MADIS, and MADIS ingests them (MADIS page: CWOP
has sent data to MADIS since 2001, data arrive every 5 minutes, late data up to
35 days are reprocessed). I found no 2026 notice that CWOP or the MADIS ingest
is ending or changing, and the MADIS pages carry no CWOP-specific change. Caveat:
the CWOP and MADIS pages are old (2016 to 2021) and I could not reach a live
status page; user forum posts that read the MesoWest sunset as "CWOP is down"
are not backed by an official statement. Consequence: the relay is safe to
build, but data will be visible through MADIS and other redistributors (NWS,
Synoptic), not MesoWest, after 2026-12-31.

## Bulk or organizational submission

No bulk, organizational or relay-on-behalf-of route is documented on any of the
CWOP, findU, APRS-IS or MADIS pages. The model is one registered identifier per
station, each station sending its own packets. A server relaying for many
owners therefore needs each owner to hold their own CWOP ID (registered at
https://www.wxqa.com/ "Join CWOP"), and relays each station under that
station's ID. Whether CWOP support will approve a single operator submitting for
many stations is not answered by documentation and stays an open question for
cwop-support (at) noaa.gov (CWOP) and madis-support (at) noaa.gov (MADIS),
which the owner should ask before the server relay goes live. The Pi-side relay
needs no approval.

## Non-ham identifiers and the APRS-IS login

- Identifiers: `CWxxxx`, `DWxxxx`, `EWxxxx`, `FWxxxx`, `GWxxxx` (two letters,
  four digits) for non-ham stations. The five character MADIS NWS ID
  (`Cxxxx` and so on) is not used in the packet; do not send an NWS ID.
- Server: connect to the designator `cwop.aprs.net`. It resolves to the CWOP
  non-ham servers (CWOP1 to CWOP4), which "do not require a validation code or
  password". Ham callsigns use `rotate.aprs.net` (core servers) or
  `rotate.aprs2.net` (Tier 2) and need a validation code obtainable from
  cwop-support.
- Port: 14580 first; if that fails, port 23.
- Login line after the server's banner line, CR LF terminated:
  `user EW9876 pass -1 vers YourSoftware 1.0`. Single spaces, software name
  and version with no spaces. The server answers with a login acknowledgement
  that can be ignored. `-1` is the APRS-IS "receive only" passcode in the
  generic spec, but CWOP's non-ham servers accept it for sending; that is the
  documented CWOP behaviour.
- Then one packet line, `EW9876>APRS,TCPIP*:...`, CR LF terminated, then
  disconnect. There is no application level acknowledgement.
- Rate: no faster than one packet per 5 minutes; 6 or 7 minutes is fine.
- The APRS-IS spec says only verified clients may send; the passcode algorithm
  is not published, so for ham callsigns the owner supplies the validation code
  and it is treated as a secret.

## Resulting plugin configuration shape

```json
{
  "relay": {
    "cwop": {
      "owner": "device",
      "station": "Acurite-5n1:1234:A",
      "id": "CW1234",
      "credential_file": "cwop.credential",
      "server": "cwop.aprs.net:14580",
      "position_opt_in": true,
      "lat": 39.1234,
      "lon": -94.5678
    }
  }
}
```

`credential_file` is empty for non-ham IDs (passcode `-1`) and names a 0600
file for ham callsigns. `server` defaults to `cwop.aprs.net:14580` for non-ham
and `rotate.aprs.net:14580` for ham IDs.

## Remaining open items

Ask CWOP and MADIS support about organizational relay. Attribution rules per
destination. Both stay in the topology page.
