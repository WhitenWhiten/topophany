# Registered Tunnel captures

These are byte-for-byte copies of the sanitized `tunnel.json` and
`connections.json` responses in the
[2026-11-02 live acceptance evidence](../../../../../../docs/acceptance/evidence/cloudflare-live-2026-11-02/provenance.json).
They came from a real registered Tunnel running cloudflared 2026.10.5, with
two sessions at `lax08` and `lax13`. Account, connector and connection
identifiers and origin addresses were removed from the responses.

The regression tests exercise both read paths, normalize these location names
to the three-letter `LAX` policy code, retain two sessions, reject an outside
city and preserve token redaction. The original acceptance failure and the
post-fix live recheck remain separate records.
