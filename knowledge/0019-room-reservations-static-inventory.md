# 0019. Room Reservations: Static Inventory, Reserve at Formation, Release on TTL or Settlement

## Status
Accepted

## Context
The delivery chain is layered: MCA forms matches, the Velocity proxy
routes players to the target sub-server, and BedwarsRel-MCA places them
into a concrete arena (room) inside that sub-server. Formed groups
therefore need a room, not just a server address, and the room must be
known to be free when the players arrive.

## Decision
- Static inventory (config `matchmaking.servers`, 1.11.0): sub-servers
  with address and room list (name + capacity). Room names live in the
  TARGET SUB-SERVER'S namespace. Startup validation rejects empty
  inventories, duplicate sub-server/room names, empty room lists, bad
  addresses, non-positive capacities. Absent `servers` keeps the 1.9.x
  behavior exactly (no room layer).
- Reserve at formation (author decision 3C): `commitGroup` inserts a
  `matchmaking_room_bindings(server_name, room_name, group_id,
  reserved_at)` row (migration v4) in the same transaction; the
  `(server_name, room_name)` primary key makes concurrent reservation of
  one room impossible (losers get 1062 and try the next eligible room —
  capacity >= mode total players, inventory order). With no eligible free
  room the whole group is rolled back and the players stay queued
  (`errNoRoomAvailable` is not a failure). This is a LOGICAL reservation
  in MCA; game servers keep no physical lock.
- Release (author decision 4B): the engine tick deletes bindings older
  than `matchmaking.room_reservation_ttl_seconds` (default 900s,
  database-side timing), and a settlement report carrying
  `matchmaking_group_id` deletes that group's binding in the settlement
  transaction ("settled = consumed", same as assignment validity).
- Assignment surface (contract 1.10.0): with the room layer a valid
  assignment carries BOTH `join_target` (the bound sub-server's inventory
  address) and `room`; a valid group without a live binding answers
  `assigned: false, reason: no_room_available` — never a stale or
  fabricated room. `/api/matchmaking/status` formed groups carry `room`
  and the bound address the same way (global `join_target` remains the
  fallback for unbound groups).

## Consequences
- Formations block (players keep queueing) while every eligible room is
  reserved; throughput is bounded by inventory size and TTL.
- Expired-but-uncleaned bindings can delay room reuse by at most one
  engine tick (the PK treats them as occupied until the cleanup DELETE).
- Reporters that do not echo `matchmaking_group_id` never release rooms
  early; TTL is the backstop (game-side reporters currently cannot know
  the group id without the BedwarsRel-side integration, spec pending in
  BedwarsRel-MCA knowledge).
- VelocityGate needs no change: `join_target` semantics are unchanged.

## Evidence
- config/config.go::MatchmakingConfig — Servers/RoomReservationTTLSeconds, validation, 1.11.0 migration
- migrate/m004_room_bindings.go::applyV4 — matchmaking_room_bindings DDL
- internal/service/matchmaking.go::reserveRoom — in-transaction reservation, 1062 fallthrough
- internal/service/matchmaking.go::Tick — expired-binding cleanup
- internal/service/matchmaking.go::Assignment — room lookup and no_room_available
- internal/service/matchmaking.go::attachRoomsAndJoinTargets — status enrichment
- internal/service/match.go::Report — settlement releases the binding
- HA-Contract docs/api/openapi/matchcorearena-business.yaml (version 1.10.0, MatchAssignment.room/reason, FormedGroup.room)
- test/e2e/e2e_test.go::TestMatchCoreE2E — "room reservation contention and settlement release" subtest
