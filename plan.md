kelay is a pubkey to nostr relay adapter.

# Environment variables

- BROKER_RELAYS - a comma-separated list of nostr relay urls to send/receive messages on
- RELAY_BACKEND - a nostr relay url to forward messages to/from
- KELAY_SECRET - a nostr hex private key

# Dependencies

Use https://github.com/nbd-wtf/go-nostr for all nostr related functionality.

# Receiving incoming events

1. Derive `kelayPubkey` from `KELAY_SECRET`
2. Create a long-running subscription to `BROKER_RELAYS` for kind 21059 events p-tagged with `kelayPubkey`
3. Kind 21059 works the same way as kind 1059, described in https://github.com/nostr-protocol/nips/blob/master/59.md
4. Unwrap the kind 21059 envelope. The inner event must be a kind 1507. If it isn't discard it.
5. The `pubkey` of the inner event is the pubkey of the nostr user. The `content` is a standard nostr message, for example `["REQ", "subid", {"kinds": [1]}]`.
6. For each user pubkey, maintain a connection to `RELAY_BACKEND`. Time them out after 30 seconds of inactivity.
7. For each received kind 1507 event, parse the nostr message in its `content` and forward it to `RELAY_BACKEND`.
8. For each message received from `RELAY_BACKEND`, create a kind 1508 with `content` equal to the JSON-encoded message.
9. Wrap the kind 1508 in a kind 21059 event using NIP 59, addressed to the user pubkey.
10. Send this event to `BROKER_RELAYS`.
