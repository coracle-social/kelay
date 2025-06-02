# Kelay

Kelay is an experimental pubkey-based proxy for relays. This is intended to piggy-back on public-facing relays as a transport mechanism for relays running on a local network. It applies the idea behind DVMs to running non-public relays.

Kelays accept any message that would normally be sent to a relay, but accept them not over a websocket connection, but via gift-wrapped messages.

1. User creates the message they want to send (for example, a `REQ`)
2. User sets this to the `content` field of a `kind 1507` unsigned event
3. User wraps it in a `kind 21059` gift wrap p-tagged for the kelay, following nip 59
5. User publishes the gift wrap to a broker relay
6. The kelay listening to the broker relay will download the `kind 21059`
7. The kelay unwraps the `kind 21059` to get the `kind 1507` rumor, following nip 59
8. The kelay sends the `content` of the `kind 1507` to the relay it's proxying (or handles it itself)
9. The kelay forwards any messages received from the relay backend by setting them to the `content` of a `kind 1508`, wrapping it in a `kind 21059` following nip 59, and publishing it to the broker relay.
10. User listens for `kind 21059` events p-tagged to them, unwraps them using nip 59, and reads the `content` of the rumor.
