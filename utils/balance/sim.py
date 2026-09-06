#!/usr/bin/env python3
"""
Balance calculator / mini-simulator for docs/UNITS.md.

Parses the "UNIT STATS TABLE" straight out of docs/UNITS.md (single source
of truth — never duplicate numbers here) and recomputes Attack Cycle / DPS /
EHP from first principles, so a typo or hand-arithmetic slip in the doc
shows up as a mismatch instead of silently propagating.

UtilityBonus and RangeTier are prose lists in the doc (not tabular), so they
are kept here as a small companion table that must be updated by hand when
the doc changes — the script warns if a unit from the stats table has no
entry here.

Usage:
    python3 utils/balance/sim.py table            # recompute + diff vs doc
    python3 utils/balance/sim.py duel Spearman "Axe Warrior"
    python3 utils/balance/sim.py map --territories 25 --footprint 150 --cross-seconds 90
    python3 utils/balance/sim.py distances         # every range/speed in server world units
"""

import argparse
import re
import sys
from dataclasses import dataclass
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
UNITS_MD = REPO_ROOT / "docs" / "UNITS.md"

# --- companion data: not in the Unit Stats Table, kept in sync by hand ---
# RangeTier: 0 = melee short (<1.3m), 1 = melee long (1.3-2.0m), 3 = ranged (>3m)
RANGE_TIER = {
    "Citizen": 0,
    "Spearman": 1,
    "Archer (Guard)": 3,
    "Guard Swordsman": 0,
    "Greatsword (Guard)": 1,
    "Axe Warrior": 0,
    "Caped Warrior": 0,
    "Rogue": 0,
    "Skullcap Warrior": 0,
    "Heavy Knight": 1,
    "Paladin": 1,
}

UTILITY_BONUS = {
    "Citizen": 0,
    "Spearman": 4 + 1,        # brace/kneel stance + weak melee-only block (25%/0%)
    "Archer (Guard)": 0 + 1,  # weak melee-only block (15%/0%, mostly symbolic)
    "Guard Swordsman": 6,     # real shield block, formation bonus (stamina-only, not scored)
    "Greatsword (Guard)": 8 + 2,  # cleave + decent melee-only parry (30%/0%, two-handed weapon)
    "Axe Warrior": 8 + 6 + 3,     # anti-shield spec (now also drains blocker stamina) + real shield block (50%/70%) + Opportunist Bow
    "Caped Warrior": 2 + 3 + 6,   # light parry + Opportunist Bow + mobility/raid spec
    "Rogue": 8 + 3 + 6 + 1,       # Quiver+execute + Recon + raid mobility spec + weak melee-only block (15%/0%)
    "Skullcap Warrior": 6 + 3,    # heavy shield block + Opportunist Bow
    "Heavy Knight": 8 + 9 + 6,    # cleave + piercing Dash-Thrust + real shield block (55%/75%, stacks with passive armor)
    "Paladin": 8 + 9 + 6,
}

# Passive DR (always-on armor, baked into EHP) — see UNITS.md section 5, EHP formula.
# Everyone else's block DR is situational (only while holding RMB) so it stays out
# of the EHP baseline and shows up as UtilityBonus instead.
PASSIVE_DR = {
    "Heavy Knight": 0.25,
    "Paladin": 0.27,
}

# Block DR (melee, ranged) — active only while holding RMB (frontal arc) AND
# standing still (GDD p.54), see UNITS.md Unit Stats Table footnotes 1/4. Hand-
# kept here like PASSIVE_DR; must stay in sync with UNITS.md's DR column.
# ALL non-Citizen units can block in melee now — the split is:
#   shield tier (also blocks ranged, rangedDR > meleeDR, low-ish drain/sec):
BLOCK_DR = {
    "Guard Swordsman": (0.70, 0.90),
    "Caped Warrior": (0.35, 0.50),
    "Skullcap Warrior": (0.60, 0.80),
    "Axe Warrior": (0.50, 0.70),
    "Heavy Knight": (0.55, 0.75),   # stacks with PASSIVE_DR (always-on armor)
    "Paladin": (0.57, 0.77),        # stacks with PASSIVE_DR (always-on armor)
}
#   no-shield tier (melee only, arrows fully bypass block, high drain/sec):
BLOCK_DR_MELEE_ONLY = {
    "Spearman": 0.25,
    "Archer (Guard)": 0.15,
    "Greatsword (Guard)": 0.30,
    "Rogue": 0.15,
}
# Axe Warrior anti-block specialization: ×2 damage vs ANY blocking target
# (SIEGE_MULTIPLIER-adjacent but vs players — see UNITS.md footnote 2) PLUS an
# extra flat stamina drain forced onto the blocker per landed hit, on top of
# their own block_drain/sec — makes sustained turtling against Axe Warrior
# specifically unsustainable, not just costly.
AXE_ANTI_BLOCK_DMG_MULT = 2.0
AXE_ANTI_BLOCK_STAMINA_DRAIN_BONUS = 20

# Resource-equivalent weights (see UNITS.md section 7, Resource Value / IronEq)
IRON_EQ_WEIGHTS = {"W": 0.4, "St": 0.6, "I": 1.0}

# Cost per unit as printed in the BalanceRatio table (section 7) — prose, not
# tabular either, so kept here alongside UtilityBonus.
COST = {
    "Spearman": {"W": 2, "I": 3},
    "Archer (Guard)": {"W": 3, "I": 1},
    "Guard Swordsman": {"W": 2, "I": 2},
    "Greatsword (Guard)": {"W": 2, "I": 6},
    "Axe Warrior": {"W": 2, "I": 4},
    "Caped Warrior": {"W": 3, "I": 2},
    "Rogue": {"W": 3, "I": 3},
    "Skullcap Warrior": {"W": 3, "I": 3},
    "Heavy Knight": {"I": 10},
    "Paladin": {"I": 10, "St": 2},
}


@dataclass
class UnitStats:
    name: str
    hp: float
    move: float
    range_type: str
    range_m: float
    dmg: float
    windup: float
    active: float
    recovery: float
    doc_t: float
    doc_dps: float

    @property
    def cycle_time(self) -> float:
        return self.windup + self.active + self.recovery

    @property
    def dps(self) -> float:
        return self.dmg / self.cycle_time


def parse_unit_stats_table(md_text: str) -> list[UnitStats]:
    """Pull rows out of the '# N. UNIT STATS TABLE' markdown table."""
    m = re.search(r"^# \d+\. UNIT STATS TABLE\b", md_text, re.MULTILINE)
    if not m:
        raise ValueError("UNIT STATS TABLE section not found in UNITS.md")
    tail = md_text[m.end():]
    end = re.search(r"^# \d+\. ", tail, re.MULTILINE)
    section = tail[: end.start()] if end else tail

    rows = []
    for line in section.splitlines():
        line = line.strip()
        if not line.startswith("|") or line.startswith("|---"):
            continue
        cells = [c.strip() for c in line.strip("|").split("|")]
        if cells[0] == "Unit":
            continue
        name = re.sub(r"[¹²³⁴⁵]", "", cells[0]).strip()
        try:
            hp = float(cells[1])
        except ValueError:
            continue  # header/garbage row
        dr_raw = cells[2]
        move = float(cells[3])
        range_match = re.match(r"(Melee|Ranged)\s+([\d.]+)", cells[4])
        range_type, range_m = (range_match.group(1), float(range_match.group(2))) if range_match else ("", 0.0)
        dmg_raw = re.sub(r"[¹²³⁴⁵]", "", cells[5])
        dmg = float(re.match(r"[\d.]+", dmg_raw).group())
        windup = float(cells[6])
        active = 0.0 if cells[7].strip() == "—" else float(cells[7])
        recovery = float(cells[8])
        doc_t = float(cells[9])
        doc_dps = float(cells[10])
        rows.append(UnitStats(name, hp, move, range_type, range_m, dmg, windup, active, recovery, doc_t, doc_dps))
    return rows


# --- Stamina (UNITS.md section 8) — prose table, kept here by hand. ---
# pool, regen/sec, optionally swing_cost (flat cost per attack; None = draw-hold/
# no flat cost) AND/OR block_drain (cost/sec while holding block — ALL non-
# Citizen units can block in melee now; see BLOCK_DR / BLOCK_DR_MELEE_ONLY).
# ASSUMPTION (not yet written down anywhere else): regen is suspended while
# actively spending stamina (attacking/blocking) — it only ticks while idle.
# This is what makes "drain 12/sec, regen 14/sec" for Skullcap Warrior mean
# something (block is NOT nearly free at a net -2/sec) instead of nothing.
# If that assumption is wrong, cmd_stamina's numbers are wrong along with it.
STAMINA = {
    "Spearman": {"pool": 100, "regen": 10, "swing_cost": 15, "block_drain": 20},
    "Archer (Guard)": {"pool": 70, "regen": 12, "swing_cost": None, "block_drain": 26},  # draw-hold, not a flat cost
    "Guard Swordsman": {"pool": 100, "regen": 10, "block_drain": 8},
    "Greatsword (Guard)": {"pool": 110, "regen": 9, "swing_cost": 30, "block_drain": 18},
    "Axe Warrior": {"pool": 100, "regen": 10, "swing_cost": 18, "block_drain": 11},
    "Caped Warrior": {"pool": 90, "regen": 14, "block_drain": 12},
    "Rogue": {"pool": 95, "regen": 15, "swing_cost": None, "block_drain": 24},
    "Skullcap Warrior": {"pool": 100, "regen": 9, "block_drain": 7},
    "Heavy Knight": {"pool": 130, "regen": 8, "swing_cost": 25, "block_drain": 9},
    "Paladin": {"pool": 135, "regen": 8, "swing_cost": 25, "block_drain": 9},
}

# --- Territory passive/building production, GDD p.15-19 (Wood/min etc). ---
PRODUCTION_PER_MIN = {
    "Forest": {"Wood": 2},
    "Sawmill": {"Wood": 10},
    "Rocky": {"Stone": 2},
    "Quarry": {"Stone": 10},
    "Iron territory": {"Iron": 1},
    "Mine": {"Iron": 9},  # GDD says 8-10/min, using the midpoint
}

# --- Siege damage multipliers, GDD p.47 (Siege Damage). Everyone not listed
# does the "minimal/symbolic" 0.1x the doc describes in prose. ---
SIEGE_MINIMAL = 0.1
# Stone is deliberately NOT in this table for any regular unit — every player
# unit does SIEGE_MINIMAL vs Stone. Stone structures require Catapult (below).
# This is a hard design choice (see conversation): wood falls to enough
# players + time, stone falls only to a built siege engine.
SIEGE_MULTIPLIER = {
    "Axe Warrior": {"Wood": 2.0, "Stone": SIEGE_MINIMAL},
}
# Fire Arrow isn't in the Unit Stats Table (it's an Archer alt-ammo mode with
# its own damage) — modeled as a separate pseudo-unit for the siege command.
FIRE_ARROW = {"dmg": 18, "cycle": None, "wood_mult": 2.0, "stone_mult": SIEGE_MINIMAL}

# Catapult: siege equipment (built via Officer blueprint + Citizen construction,
# like Battering Ram, GDD p.48), not a combat unit — the ONLY real source of
# Stone damage. Zero damage vs Wood (not its job) and zero vs players.
CATAPULT = {"dmg": 800, "reload_s": 10, "wood_mult": 0.0, "stone_mult": 1.0}

# No artificial attacker cap: more players hitting a structure deal
# proportionally more DPS, full stop (GDD p.47 — deliberately rejected a
# frontage/ranged cap formula; the only limit on melee crowding is emergent
# player-hitbox collision on the arena itself, not a game rule). The low-pop
# "grinding a wall for hours" concern is instead solved by picking sane flat
# HP numbers (a small dedicated squad already breaches Wood in ~5 min) plus
# Active Siege Repair (GDD p.46), not by capping attacker count.

# Distances/ranges mentioned in UNITS.md prose (not in the tabular Unit Stats
# Table), for the "distances" command. Kept here by hand, same caveat as
# RANGE_TIER/UTILITY_BONUS above.
PROSE_DISTANCES_M = {
    "Opportunist Bow range (Axe/Caped/Skullcap Warrior)": 6.0,
    "Rogue Quiver range": 6.0,
    "Fire Arrow range (Archer, same as normal shot)": 9.0,
    "Dash-Thrust distance (Heavy Knight/Paladin)": 3.0,
    "Rogue Recon: 'don't ping Enemy Activity' threshold": 15.0,
}


def cmd_table(args):
    text = UNITS_MD.read_text(encoding="utf-8")
    units = parse_unit_stats_table(text)

    print(f"{'Unit':<20} {'T(doc)':>7} {'T(calc)':>8} {'DPS(doc)':>9} {'DPS(calc)':>10} {'EHP':>6} {'Threat':>7} {'IronEq':>7} {'Ratio':>6}")
    mismatches = 0
    for u in units:
        calc_t = round(u.cycle_time, 3)
        calc_dps = round(u.dps, 1)
        t_flag = "" if abs(calc_t - u.doc_t) < 0.01 else "  <-- MISMATCH"
        dps_flag = "" if abs(calc_dps - u.doc_dps) < 0.15 else "  <-- MISMATCH"
        if t_flag or dps_flag:
            mismatches += 1

        passive_dr = PASSIVE_DR.get(u.name, 0.0)
        ehp = u.hp / (1 - passive_dr)  # block DR (everyone else) is situational, not baked in
        range_tier = RANGE_TIER.get(u.name)
        utility = UTILITY_BONUS.get(u.name)
        threat = ratio = iron_eq = None
        if range_tier is not None and utility is not None:
            threat = calc_dps + ehp / 10 + range_tier * 3 + utility
            cost = COST.get(u.name, {})
            iron_eq = sum(IRON_EQ_WEIGHTS.get(k, 0) * v for k, v in cost.items())
            ratio = threat / iron_eq if iron_eq else None

        print(
            f"{u.name:<20} {u.doc_t:>7.2f} {calc_t:>8.2f}{t_flag:<14} "
            f"{u.doc_dps:>9.1f} {calc_dps:>10.1f}{dps_flag:<14} "
            f"{ehp:>6.0f} "
            f"{threat if threat is not None else float('nan'):>7.1f} "
            f"{iron_eq if iron_eq is not None else float('nan'):>7.1f} "
            f"{ratio if ratio is not None else float('nan'):>6.1f}"
        )
        if range_tier is None or utility is None:
            print(f"  (!) no RangeTier/UtilityBonus entry for '{u.name}' in sim.py — add one")

    print()
    if mismatches:
        print(f"{mismatches} unit(s) have T/DPS in UNITS.md that don't match Dmg/Windup/Active/Recovery — fix the doc or this script.")
    else:
        print("All T/DPS values in UNITS.md are internally consistent with their own Windup/Active/Recovery/Dmg.")


def time_to_kill(attacker: "UnitStats", defender: "UnitStats") -> float:
    return defender.hp / attacker.dps


def resolve_unit(units: dict, name: str) -> "UnitStats":
    if name in units:
        return units[name]
    matches = [u for u in units.values() if name.lower() in u.name.lower()]
    if len(matches) == 1:
        return matches[0]
    raise SystemExit(f"Unit '{name}' not found or ambiguous. Options: {list(units)}")


def cmd_duel(args):
    text = UNITS_MD.read_text(encoding="utf-8")
    units = {u.name: u for u in parse_unit_stats_table(text)}

    a, b = resolve_unit(units, args.unit_a), resolve_unit(units, args.unit_b)

    ttk_a_kills_b = time_to_kill(a, b)
    ttk_b_kills_a = time_to_kill(b, a)

    print(f"{a.name}: DPS={a.dps:.1f}  HP={a.hp:.0f}  cycle={a.cycle_time:.2f}s")
    print(f"{b.name}: DPS={b.dps:.1f}  HP={b.hp:.0f}  cycle={b.cycle_time:.2f}s")
    print()
    print(f"Time for {a.name} to kill {b.name} (uncontested, no block/dodge/miss): {ttk_a_kills_b:.2f}s")
    print(f"Time for {b.name} to kill {a.name} (uncontested, no block/dodge/miss): {ttk_b_kills_a:.2f}s")
    winner = a.name if ttk_a_kills_b < ttk_b_kills_a else b.name
    print(f"\nIn a pure stand-and-trade with zero skill expression, {winner} kills first.")
    print("Caveat: ignores block/DR, dodge, range/positioning, Opportunist Bow, Dash-Thrust —")
    print("this is a raw DPS-race sanity check, not a matchup predictor. Use it to catch")
    print("obviously broken numbers, not to settle counter-matrix arguments.")


def cmd_matchup(args):
    """All-vs-all raw stand-and-trade TTK grid. Same caveats as `duel`, applied
    to every pair at once — the point is spotting systemic outliers, not
    predicting any single real fight."""
    text = UNITS_MD.read_text(encoding="utf-8")
    units = [u for u in parse_unit_stats_table(text) if u.name != "Citizen"]
    names = [u.name for u in units]
    short = {n: (n[:14] + "…") if len(n) > 15 else n for n in names}

    # ttk[i][j] = time for units[i] to kill units[j]
    ttk = [[time_to_kill(a, b) if a is not b else float("nan") for b in units] for a in units]

    print("Rows attack columns. Cell = seconds for ROW to kill COLUMN (lower = row is scarier).\n")
    header = f"{'':<16}" + "".join(f"{short[n]:>16}" for n in names)
    print(header)
    for i, a in enumerate(units):
        row = f"{short[a.name]:<16}"
        for j in range(len(units)):
            row += f"{'':>16}" if i == j else f"{ttk[i][j]:>16.2f}"
        print(row)

    print("\n--- Dominance ranking ---")
    print("wins = pairs where this unit kills the other one faster than the other kills it back")
    print("(pure DPS-race outcome — ignores range/positioning/counters entirely)\n")
    scores = []
    for i, a in enumerate(units):
        wins = sum(1 for j in range(len(units)) if i != j and ttk[i][j] < ttk[j][i])
        scores.append((wins, a.name))
    scores.sort(reverse=True)
    for wins, name in scores:
        print(f"{name:<20} beats {wins:>2}/{len(units)-1} other units in a stand-and-trade")

    print("\nA unit winning ~all of its trades is not automatically broken — Counter Matrix")
    print("counters (reach, block, mobility, Opportunist Bow, Dash-Thrust) live outside this")
    print("model on purpose. But if the SAME unit tops this list AND has no listed counter in")
    print("UNITS.md, that combination is worth a second look.")


def cmd_approach(args):
    """How much damage a stationary Archer lands on each melee unit while it
    closes the distance from the Archer's max range to its own melee range —
    answers 'is the Archer an unkillable kite machine' directly, instead of
    guessing from DPS/range numbers alone.

    Model: Archer stands still at --archer-range (default: its own Range stat,
    9.0m) and fires on its normal cycle (first shot lands after Windup, then
    every cycle_time). The attacker closes at its Move speed until it's within
    its own melee Range, then the clock stops — no shots land during the swing
    that kills the Archer, this is purely 'damage taken while closing'.

    DR while closing: only passive armor (Heavy Knight/Paladin) applies. Block
    (Guard Swordsman/Skullcap Warrior/Caped Warrior) is confirmed GDD p.54 to
    require standing still — moving drops it instantly — so it CANNOT reduce
    damage taken while covering ground, only while holding a static position.
    That's a real, load-bearing rule change (not just a caveat): it means the
    Counter Matrix's 'Archer weak vs Shield' only holds for a shield-holder
    already defending a position under fire, not one charging across open
    ground to close the gap — see UNITS.md DR footnote 1."""
    text = UNITS_MD.read_text(encoding="utf-8")
    units = {u.name: u for u in parse_unit_stats_table(text)}
    archer = resolve_unit(units, "Archer")

    archer_range = args.archer_range if args.archer_range else archer.range_m
    print(f"Archer (Guard): range {archer_range:.1f}m, dmg {archer.dmg:.0f}/shot, cycle {archer.cycle_time:.2f}s "
          f"(windup {archer.windup:.2f}s), stationary — no kiting modeled.\n")

    targets = [u for u in units.values() if u.name != archer.name and u.range_type == "Melee"]
    if args.attacker:
        targets = [resolve_unit(units, args.attacker)]

    header = f"{'Unit':<20}{'Move':>6}{'MeleeRng':>9}{'Close(m)':>9}{'CloseTime':>10}{'Shots':>7}{'DR':>5}{'DmgTaken':>9}{'HP':>6}{'HP left':>9}{'Survives?':>10}"
    print(header)
    for u in targets:
        close_dist = max(0.0, archer_range - u.range_m)
        close_time = close_dist / u.move if u.move > 0 else float("inf")
        if close_time < archer.windup:
            shots = 0
        else:
            shots = int((close_time - archer.windup) // archer.cycle_time) + 1

        dr = PASSIVE_DR.get(u.name, 0.0)  # block DR does NOT apply while moving, GDD p.54
        dmg_taken = shots * archer.dmg * (1 - dr)
        hp_left = u.hp - dmg_taken
        survives = "yes" if hp_left > 0 else "DIES"
        print(f"{u.name:<20}{u.move:>6.1f}{u.range_m:>9.1f}{close_dist:>9.1f}{close_time:>10.2f}{shots:>7d}"
              f"{dr*100:>4.0f}%{dmg_taken:>9.1f}{u.hp:>6.0f}{hp_left:>9.1f}{survives:>10}")

    print("\nCaveat: ignores lateral movement/dodging by the attacker, the Archer's own Opportunist")
    print("Bow (n/a, Archer doesn't have one) and Fire Arrow, retreat/kiting by the Archer once the")
    print("attacker gets close (a moving Archer buys more shots, this models worst-case-for-Archer:")
    print("standing ground). Use --archer-range to test shorter effective ranges (obstacles, curved")
    print("sightlines, etc). ALL units can block now (GDD p.54), including the 4 without a real")
    print("shield (Spearman/Archer/Greatsword/Rogue — melee-only, see BLOCK_DR_MELEE_ONLY) — but")
    print("block requires standing still, so nobody gets any DR here while closing distance, shield")
    print("or not. It only matters once they stop, either at melee range or holding a static position.")


def cmd_map(args):
    # Everything here works in real meters first, then converts to server
    # integer units via --units-per-meter — that's the actual design lever
    # (see conversation: uint16 X/Y, "1 unit = 1 step, no fractions").
    text = UNITS_MD.read_text(encoding="utf-8")
    units = parse_unit_stats_table(text)
    move_speeds = {u.name: u.move for u in units if u.move > 0}

    speed_name = args.speed_unit
    matches = [n for n in move_speeds if speed_name.lower() in n.lower()]
    if not matches:
        raise SystemExit(f"--speed-unit '{speed_name}' not found. Options: {list(move_speeds)}")
    speed_mps = move_speeds[matches[0]]

    upm = args.units_per_meter
    uint16_max_m = 65535 / upm

    print(f"Reference speed: {matches[0]} at {speed_mps} m/s (from UNITS.md)")
    print(f"Resolution: 1 world unit = {1/upm*100:.1f} cm  ({upm} units/meter)")
    print(f"uint16 range at this resolution: {uint16_max_m:.0f} m ({uint16_max_m/1000:.2f} km) per axis, per region")

    units_per_tick = speed_mps * upm / args.tick_rate
    whole = round(units_per_tick)
    print(f"\nPlayerSpeedPerTick @ {args.tick_rate} ticks/s = {speed_mps} * {upm} / {args.tick_rate} = {units_per_tick:.2f} units/tick")
    if abs(units_per_tick - whole) > 0.01:
        print(f"  -> not a whole number. Standard fix: accumulate a fractional remainder each tick")
        print(f"     (e.g. fixed-point sub-unit accumulator) and flush whole units — normal technique,")
        print(f"     not a real blocker. Nearest whole-tick alternative: {whole} units/tick "
              f"({whole * args.tick_rate / upm:.3f} m/s effective).")
    else:
        print(f"  -> whole number, no remainder-accumulation needed.")

    speed_units_per_sec = speed_mps * upm
    max_side_m = speed_mps * args.cross_seconds
    print(f"\nAt {speed_mps} m/s, crossing a straight-line map side in <= {args.cross_seconds}s needs side <= {max_side_m:.0f} m")

    import math
    footprint_m = args.footprint
    side_for_territories_m = math.sqrt(args.territories * footprint_m ** 2 * args.slack)
    print(f"\n{args.territories} territories x {footprint_m}m footprint x {args.slack} slack factor")
    print(f"  -> suggested map side: ~{side_for_territories_m:.0f} m ({side_for_territories_m/1000:.2f} km)")
    print(f"     = {side_for_territories_m*upm:.0f} world units at this resolution")

    if side_for_territories_m > max_side_m:
        print(
            f"\n(!) Territory-driven size ({side_for_territories_m:.0f}m) exceeds the "
            f"{args.cross_seconds}s crossing budget ({max_side_m:.0f}m).\n"
            f"    Either shrink footprint/territory count, raise move speed, or accept a "
            f"longer crossing time (contradicts GDD p.58 'find the war quickly')."
        )
    else:
        print(f"\nFits inside the {args.cross_seconds}s crossing budget with room to spare.")

    if side_for_territories_m > uint16_max_m:
        print(
            f"\n(!) Suggested map side ({side_for_territories_m:.0f}m) exceeds what a single uint16 "
            f"region can hold at this resolution ({uint16_max_m:.0f}m).\n"
            f"    This is fine IF the map is region-sharded (each territory/battle = its own local "
            f"uint16 space, per the GDD's 'simulation-region' concept) — only a problem for one "
            f"giant flat coordinate space."
        )
    else:
        print(f"\nFits inside a single uint16 region at this resolution — no uint32 widening needed.")

    print(f"\nCurrent gameConfig.json world (6000x3000 units) is a movement-netcode test arena,")
    print(f"not sized for the campaign map — treat these numbers as the starting point instead.")


def cmd_distances(args):
    """Convert every distance/speed in UNITS.md to server world units at the
    agreed resolution (GDD p.59, World Coordinate Resolution: 1 unit = 0.1m)."""
    text = UNITS_MD.read_text(encoding="utf-8")
    units = parse_unit_stats_table(text)
    upm = args.units_per_meter

    print(f"Resolution: 1 world unit = {1/upm*100:.1f} cm ({upm} units/meter)\n")

    print("--- Weapon ranges (Unit Stats Table) ---")
    print(f"{'Unit':<20} {'Type':<7} {'meters':>7} {'world units':>12}")
    for u in units:
        if u.range_m <= 0:
            continue
        print(f"{u.name:<20} {u.range_type:<7} {u.range_m:>7.1f} {u.range_m*upm:>12.0f}")

    print("\n--- Move speed -> PlayerSpeedPerTick @ {} ticks/s ---".format(args.tick_rate))
    print(f"{'Unit':<20} {'m/s':>6} {'units/tick':>11} {'whole?':>7}")
    for u in units:
        if u.move <= 0:
            continue
        upt = u.move * upm / args.tick_rate
        whole = "yes" if abs(upt - round(upt)) < 0.01 else f"no (~{round(upt)})"
        print(f"{u.name:<20} {u.move:>6.1f} {upt:>11.2f} {whole:>7}")

    print("\n--- Other distances mentioned in UNITS.md prose ---")
    print(f"{'Distance':<55} {'meters':>7} {'world units':>12}")
    for label, meters in PROSE_DISTANCES_M.items():
        print(f"{label:<55} {meters:>7.1f} {meters*upm:>12.0f}")

    print(f"\nAll values are the direct product of meters * {upm} — recompute with --units-per-meter")
    print(f"if the resolution decision in GDD p.59 ever changes.")


def cmd_stamina(args):
    print("ASSUMPTION: regen is suspended while attacking/blocking, only ticks while idle.")
    print("This is NOT written down in UNITS.md yet — if the real design intends regen to run")
    print("concurrently with block-drain, every number below is wrong. Flagging it explicitly")
    print("because e.g. Skullcap Warrior's 7/sec drain vs 9/sec regen would be near-free block")
    print("under a concurrent model, which contradicts its own 'not for holding the line' text.\n")

    for name, s in STAMINA.items():
        pool, regen = s["pool"], s["regen"]
        print(f"{name}: pool={pool}  regen={regen}/s")
        if s.get("swing_cost"):
            cost = s["swing_cost"]
            max_swings = pool // cost
            refill = pool / regen
            print(f"  max consecutive swings from full: {max_swings}  (cost {cost}/swing)")
            print(f"  full refill after exhausting: {refill:.1f}s")
        elif "swing_cost" in s:
            print(f"  (draw-hold / no flat swing cost modeled — see UNITS.md prose)")
        if s.get("block_drain"):
            drain = s["block_drain"]
            hold_from_full = pool / drain
            duty_cycle = regen / (drain + regen) * 100
            print(f"  continuous block hold from full: {hold_from_full:.1f}s  ({drain}/s drain)")
            print(f"  sustainable block duty cycle (alternate hold/release forever): {duty_cycle:.0f}% of time")
        print()


def cmd_economy(args):
    income = {}
    for building, count in [("Sawmill", args.sawmill), ("Quarry", args.quarry), ("Mine", args.mine),
                             ("Forest", args.forest), ("Rocky", args.rocky), ("Iron territory", args.iron_territory)]:
        if count <= 0:
            continue
        for res, rate in PRODUCTION_PER_MIN[building].items():
            income[res] = income.get(res, 0) + rate * count

    print("Income/min from the given buildings:")
    for res, rate in income.items():
        print(f"  {res}: {rate}/min")
    if not income:
        print("  (none — pass --sawmill/--quarry/--mine/--forest/--rocky/--iron-territory counts)")
    print()

    text = UNITS_MD.read_text(encoding="utf-8")
    units = {u.name: u for u in parse_unit_stats_table(text) if u.name != "Citizen"}
    targets = [resolve_unit(units, args.unit)] if args.unit else list(units.values())

    print(f"{'Unit':<20} {'Cost':<14} {'Sustainable/min':>16}  {'bottleneck'}")
    for u in targets:
        cost = COST.get(u.name, {})
        if not cost:
            continue
        cost_str = " + ".join(f"{v}{k}" for k, v in cost.items())
        limits = []
        for res_key, res_name in [("W", "Wood"), ("St", "Stone"), ("I", "Iron")]:
            amount = cost.get(res_key, 0)
            if amount > 0 and income.get(res_name, 0) > 0:
                limits.append((income[res_name] / amount, res_name))
            elif amount > 0:
                limits.append((0.0, res_name + " (no income)"))
        if not limits:
            print(f"{u.name:<20} {cost_str:<14} {'n/a (free)':>16}")
            continue
        rate, bottleneck = min(limits)
        print(f"{u.name:<20} {cost_str:<14} {rate:>16.2f}  {bottleneck}")

    print("\nThis is pure income/cost throughput — it ignores Local Stockpile buffering,")
    print("manual gathering, and that units cost 0% back on death (GDD p.32-33) — it answers")
    print("'can this front indefinitely replace losses of unit X', not 'can I burst 10 right now'.")


def parse_attackers(units: dict, attackers_str: str, structure_type: str, scale: float = 1.0):
    """Returns (total_dps, N_attackers, breakdown_lines) for an attacker mix
    string like 'Axe Warrior:5,Fire Arrow:2,Catapult:1', with counts multiplied
    by `scale` and rounded (used by `siege` and `siegescale`).

    No attacker cap: more players deal proportionally more DPS, full stop —
    GDD p.47 deliberately rejects any formula-based crowd limit."""
    entries = []  # (name, count)
    for entry in attackers_str.split(","):
        name, _, count_str = entry.partition(":")
        base_count = int(count_str) if count_str else 1
        count = max(0, round(base_count * scale))
        name = name.strip()
        entries.append((name, count))

    total_dps = 0.0
    n_total = 0
    lines = []
    for name, count in entries:
        n_total += count

        if name.lower() in ("fire arrow", "archer (fire arrow)"):
            archer = resolve_unit(units, "Archer")
            cycle = archer.cycle_time
            mult = FIRE_ARROW["wood_mult"] if structure_type == "Wood" else FIRE_ARROW["stone_mult"]
            dps = FIRE_ARROW["dmg"] / cycle * mult
            label = "Archer (Fire Arrow)"
        elif name.lower() == "catapult":
            mult = CATAPULT["wood_mult"] if structure_type == "Wood" else CATAPULT["stone_mult"]
            dps = CATAPULT["dmg"] / CATAPULT["reload_s"] * mult
            label = "Catapult"
        else:
            u = resolve_unit(units, name)
            mult = SIEGE_MULTIPLIER.get(u.name, {}).get(structure_type, SIEGE_MINIMAL)
            dps = u.dps * mult
            label = u.name

        contribution = dps * count
        total_dps += contribution
        count_str = f"{count:g}"
        lines.append(f"  {count_str}x {label:<24} {dps:.1f} dps each x{mult:.1f} mult = {contribution:.1f} dps total")
    return total_dps, n_total, lines


def cmd_siege(args):
    text = UNITS_MD.read_text(encoding="utf-8")
    units = {u.name: u for u in parse_unit_stats_table(text)}

    structure_type = args.structure_type
    if structure_type not in ("Wood", "Stone"):
        raise SystemExit("--structure-type must be 'Wood' or 'Stone'")

    hp = args.hp
    print(f"Attackers vs {structure_type} structure:")
    total_dps, n_total, lines = parse_attackers(units, args.attackers, structure_type)
    print("\n".join(lines))
    print(f"\nTotal siege DPS: {total_dps:.1f}/s  ({n_total:g} attackers, no cap — GDD p.47)")

    if hp:
        breach_time = hp / total_dps
        print(f"Structure HP {hp:.0f} -> breach in {breach_time:.0f}s ({breach_time/60:.1f} min), uncontested.")
    if args.breach_seconds:
        required_hp = total_dps * args.breach_seconds
        print(f"To breach in {args.breach_seconds}s uncontested, structure HP should be ~{required_hp:.0f}.")
    if not hp and not args.breach_seconds:
        print("\nPass --hp to compute breach time, or --breach-seconds to back-solve required HP.")
        print("GDD p.47 reference values: Wood Gate/Wall 65 000, Stone Wall/Tower 90 000, Fort 150 000.")

    print("\nIgnores Active Siege Repair (-75% repair while actively damaged, GDD p.46) and")
    print("Battering Ram (GDD p.48) entirely — uncontested chip-damage baseline only.")


def cmd_siegescale(args):
    """HP that scales with attacker count: HP(N) = HP_ref * (N/N_ref)^exponent.
    exponent=0 is today's bug (fixed HP, breach time collapses as 1/N).
    exponent=1 makes breach time constant regardless of siege size (mass never helps).
    Something in between rewards numbers without trivializing the wall."""
    text = UNITS_MD.read_text(encoding="utf-8")
    units = {u.name: u for u in parse_unit_stats_table(text)}

    structure_type = args.structure_type
    if structure_type not in ("Wood", "Stone"):
        raise SystemExit("--structure-type must be 'Wood' or 'Stone'")

    dps_ref, n_ref, lines = parse_attackers(units, args.attackers, structure_type)
    hp_ref = dps_ref * args.breach_seconds
    print(f"Reference scenario: {n_ref} attackers, {dps_ref:.1f} dps total")
    print(f"Calibrated so breach takes {args.breach_seconds:.0f}s at this size -> HP_ref = {hp_ref:.0f}\n")

    multipliers = [float(x) for x in args.multipliers.split(",")]
    exponents = [float(x) for x in args.exponents.split(",")]

    header = f"{'attackers':>10} " + "".join(f"p={p:<4}" .rjust(14) for p in exponents)
    print(header)
    for m in multipliers:
        n = round(n_ref * m)
        dps_m, _, _ = parse_attackers(units, args.attackers, structure_type, scale=m)
        row = f"{n:>10} "
        for p in exponents:
            hp_m = hp_ref * (m ** p)
            breach_m = hp_m / dps_m if dps_m > 0 else float("inf")
            row += f"{breach_m:>13.0f}s"
        print(row)

    print("\nColumns are candidate exponents p in HP(N) = HP_ref * (N/N_ref)^p.")
    print("p=0.0 : today — fixed HP, breach time = breach_seconds / m (collapses to near-zero)")
    print("p=0.5 : sqrt scaling — bigger sieges are meaningfully faster, never trivial")
    print("p=0.7 : closer to constant, still rewards mass noticeably")
    print("p=1.0 : breach time constant at every scale — pure 'coordinated effort' model, no reward for zerging")
    print("\nRecommendation: p=0.5-0.7. p=0 reproduces the '3 min at 5 players -> ~1s at 50' complaint.")
    print("p=1.0 is defensible too (matches GDD's anti-snowball spirit) but removes any incentive")
    print("to actually bring more attackers to a siege, which cuts against 'Massive Warfare' (p.2).")
    print("\nBasis for N: should be the CURRENT attacking force at this structure (or the attacking")
    print("Kingdom's online count, same pattern as Claim Capacity's `online` variable, GDD p.13) —")
    print("not a global server population, so a quiet border wall isn't buffed by an unrelated 500-")
    print("player battle happening elsewhere on the map.")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="cmd", required=True)

    sub.add_parser("table", help="Recompute Attack Cycle/DPS/ThreatScore/Ratio from UNITS.md and flag mismatches")

    p_duel = sub.add_parser("duel", help="Rough uncontested DPS-race between two units")
    p_duel.add_argument("unit_a")
    p_duel.add_argument("unit_b")

    p_map = sub.add_parser("map", help="Map-size + coordinate-resolution estimate from real meters (UNITS.md Move speeds)")
    p_map.add_argument("--territories", type=int, default=25)
    p_map.add_argument("--footprint", type=float, default=150, help="approx. meters diameter needed per territory (base + battle space)")
    p_map.add_argument("--slack", type=float, default=1.8, help="packing slack factor for irregular, non-grid territory layout")
    p_map.add_argument("--speed-unit", type=str, default="Spearman", help="which UNITS.md unit's Move speed (m/s) to use as the reference")
    p_map.add_argument("--units-per-meter", type=float, default=10, help="server coordinate resolution: 1=meter, 10=decimeter, 100=centimeter")
    p_map.add_argument("--tick-rate", type=float, default=20)
    p_map.add_argument("--cross-seconds", type=float, default=90, help="target time to cross the map in a straight line")

    p_dist = sub.add_parser("distances", help="Convert all UNITS.md ranges/speeds to server world units (GDD p.59 resolution)")
    p_dist.add_argument("--units-per-meter", type=float, default=10, help="server coordinate resolution: 1=meter, 10=decimeter, 100=centimeter")
    p_dist.add_argument("--tick-rate", type=float, default=20)

    sub.add_parser("matchup", help="All-vs-all raw stand-and-trade TTK grid + dominance ranking")

    p_approach = sub.add_parser("approach", help="Damage a stationary Archer lands on each melee unit while it closes the gap")
    p_approach.add_argument("--attacker", type=str, default=None, help="only compute for this unit (default: all melee units)")
    p_approach.add_argument("--archer-range", type=float, default=None, help="override Archer's effective range in meters (default: UNITS.md Range stat)")

    sub.add_parser("stamina", help="Block-hold duration / max swings / duty cycle from the Stamina table")

    p_econ = sub.add_parser("economy", help="Sustainable units/min from territory income vs unit cost")
    p_econ.add_argument("--sawmill", type=int, default=0)
    p_econ.add_argument("--quarry", type=int, default=0)
    p_econ.add_argument("--mine", type=int, default=0)
    p_econ.add_argument("--forest", type=int, default=0, help="plain (un-upgraded) forest territories")
    p_econ.add_argument("--rocky", type=int, default=0, help="plain (un-upgraded) rocky territories")
    p_econ.add_argument("--iron-territory", type=int, default=0, help="plain (un-upgraded) iron territories")
    p_econ.add_argument("--unit", type=str, default=None, help="show one unit only (default: all)")

    p_siege = sub.add_parser("siege", help="Breach-time / required-HP calculator for Wall/Gate/Tower/Fort (GDD p.47)")
    p_siege.add_argument("--attackers", required=True, help="e.g. 'Axe Warrior:5,Fire Arrow:2,Catapult:1'")
    p_siege.add_argument("--structure-type", required=True, choices=["Wood", "Stone"])
    p_siege.add_argument("--hp", type=float, default=None, help="structure HP -> compute breach time")
    p_siege.add_argument("--breach-seconds", type=float, default=None, help="desired breach time -> back-solve HP")

    p_scale = sub.add_parser("siegescale", help="Compare HP-vs-attacker-count scaling formulas (fixes 'wall dies in 1s at 50 players')")
    p_scale.add_argument("--attackers", required=True, help="reference-size attacker mix, e.g. 'Axe Warrior:5'")
    p_scale.add_argument("--structure-type", required=True, choices=["Wood", "Stone"])
    p_scale.add_argument("--breach-seconds", type=float, required=True, help="target breach time AT the reference attacker count")
    p_scale.add_argument("--multipliers", type=str, default="1,2,5,10,20", help="attacker-count multipliers to compare, relative to the reference")
    p_scale.add_argument("--exponents", type=str, default="0,0.5,0.7,1.0", help="candidate exponents p in HP(N)=HP_ref*(N/N_ref)^p")

    args = parser.parse_args()
    {
        "table": cmd_table,
        "duel": cmd_duel,
        "map": cmd_map,
        "distances": cmd_distances,
        "matchup": cmd_matchup,
        "approach": cmd_approach,
        "stamina": cmd_stamina,
        "economy": cmd_economy,
        "siege": cmd_siege,
        "siegescale": cmd_siegescale,
    }[args.cmd](args)


if __name__ == "__main__":
    sys.exit(main())
