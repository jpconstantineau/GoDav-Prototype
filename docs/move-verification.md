# MOVE development evidence

All logs below are retained in GoDav-Project-Tools/logs outside the checkout.
They describe the code at each invocation, not an immutable aggregate pass.

- move09-01-red-file: file MOVE Depth0 returned405 before implementation.
- move09-02-file: file move/case spelling/byte and ETag continuity passed.
- move09-03-red-replacement: replacement returned500 before atomic replacement.
- move09-04-replacement: collection overwrite and unchanged F collision passed.
- move09-05-protocol: narrow Destination/Depth/Overwrite/body/root/type/condition
  profile passed with fresh readback and namespace preservation.
- move09-06-red-lock-subtree: child lock returned423 before required207 mapping.
- move09-07-lock-subtree: child source lease removal, destination scope retention,
  tagged two-token replacement and case-only lock continuity passed.
- move09-08-lifecycle: executable reopen/creation/dead-property/byte continuity,
  no-payload-read MOVE, failed precommit and cancelled rollback/reuse passed.
- move09-09-races-bounds: concurrent collision/sibling cases passed; byte-bound
  fixture expected rejection at exactly4096 and was corrected to4097 afterward.
  That test failure was a fixture boundary error, not product failure evidence.
- move09-10-red-traces: sanitized destination field was missing before addition.
- move09-11-traces-bounds: safe completion traces and descendant depth/byte bounds
  passed with corrected one-byte-over-limit fixture.
- move09-12-combined-bounds: node/content excess, combined replacement bounds,
  exact boundary success, smaller independent moves and reopen passed.
- move09-13-publication-locks: source/destination parent membership, incoming
  destination child lease spelling, removed child lease cleanup and conditional
  upload publication across a concurrent MOVE passed.
- move09-14-focused-regression: all MOVE tests and the legacy namespace depth/
  capability test passed together (50.791 seconds). Two intended routing names
  in that selector matched no tests; the exact original names are run separately.

Further focused tests, final exact clean revision, review outcomes, race and
aggregate/release checks belong in the external sealed evidence manifest.
No native-client or Linux execution claim is made here.
