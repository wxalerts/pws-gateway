// Package collector is the pipeline between rtl_433 and the store.
//
// A Pipeline reads Lines from a Source (a rtl433.Supervisor in production, a
// rtl433.Replay in tests and demos) and pushes each one through the same
// stages: Decoder, mapping, repeat suppression, discovery.Tracker,
// aggregate.Bucketer and store.Writer. Nothing here imports the web layer;
// the web layer adapts the query methods (Stations, Readings, OutboxStatus,
// Status) to its own interfaces.
//
// # What is kept for unselected stations
//
// Every discovered station gets discovery tracking (key, model, fields and raw
// field names seen, signal, last seen), and its recent readings are held in
// memory (Config.RecentPerStation) so the owner can preview a station before
// selecting it. That is all: nothing for an unselected station is written to
// the store as a rollup, and it is never uplinked or relayed. The recent
// readings live in RAM only and are lost at restart. The station list itself
// is persisted so that a known station is still listed after a restart.
//
// Selection is applied through Config.Selection. For a selected station only
// the effective fields (config.Station.EffectiveFields) are aggregated into
// buckets and stored. Deselecting a station stops new rollups from being
// written; rollups already stored are not deleted. A bucket that was already
// open when the station was deselected is still written when it closes.
//
// # Repeat suppression
//
// Most transmitters send each message several times in a row: the real
// Acurite-5n1 capture in testdata/real shows three identical lines with the
// same timestamp per transmission, and the 609TXC shows five or six lines
// within half a second. Counting each repeat as a sighting would let a single
// burst of noise satisfy the discovery rule, so a line whose canonical and
// raw content equals the previous accepted line from the same transmitter
// within Config.DedupWindow (default 2 s) is counted as a duplicate and goes
// no further. Fields that legitimately differ between repeats
// (sequence_num, repeat) and the radio metadata are ignored in the
// comparison. A negative window disables the stage.
//
// # Unsynced time policy
//
// Readings are only stamped, tracked and aggregated against a trustworthy
// wall clock. The Pi has no battery backed clock, so after a cold boot the
// clock is wrong until chrony or systemd-timesyncd reports sync. While
// Config.TimeSynced returns false:
//
//   - every decoded reading is held in a bounded ring (Config.HoldMax,
//     default 2000) tagged with a monotonic offset taken when the line
//     arrived (Config.Mono, by default time.Since the pipeline was built).
//     The wall clock is never read for held readings.
//   - when the ring is full the oldest reading is dropped and counted
//     (Status.HoldOverflow, event EventHoldOverflow).
//
// When TimeSynced turns true the pipeline records the sync point (wall time
// and monotonic offset). Each held reading whose monotonic age at that
// moment is under Config.HoldLimit (default 15 minutes) is re-stamped as
// syncWall minus age and processed in arrival order, so stations heard
// before sync are discovered and bucketed on their true times. Older
// readings are dropped and counted (Status.HoldExpired) and one
// EventHoldExpired summarizes them. After sync, readings are stamped with
// the receipt time of the line, which for a Supervisor is the local clock
// and for a Replay is the recorded timestamp.
package collector
