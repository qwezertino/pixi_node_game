"""Summarize controlled A/B output: python3 summarize-latency.py RUN_DIR ..."""
import json
import math
import re
import sys
from pathlib import Path


def read_metrics(path):
    result = {}
    for line in path.read_text().splitlines():
        if line and not line.startswith('#'):
            key, value, *_ = line.split()
            result[key] = float(value)
    return result


for name in sys.argv[1:]:
    root = Path(name)
    before, after = (read_metrics(root / f'{part}.prom') for part in ('before', 'after'))
    delta = {key: value - before.get(key, 0) for key, value in after.items()}
    clients = json.loads((root / 'clients.json').read_text())
    seconds = clients['seconds']  # Client monotonic clock, not wall-clock timestamps.
    result = {'run': name, 'clients': clients['clients'], 'seconds': seconds, 'server': {}}
    for metric in ('game_tick_duration_seconds', 'game_tick_world_step_seconds',
                   'game_tick_fanout_send_seconds', 'game_world_state_queue_delay_seconds',
                   'game_world_state_age_at_write_end_seconds', 'game_ws_write_batch_seconds',
                   'game_tick_start_delay_seconds'):
        buckets = sorted((float(re.search(r'le="([^"]+)"', key)[1]), value)
                         for key, value in delta.items() if key.startswith(metric + '_bucket{'))
        if not buckets or not buckets[-1][1]:
            continue
        quantiles = {}
        for q in (0.5, 0.95, 0.99):
            lower = previous = 0
            for upper, count in buckets:
                if count >= q*buckets[-1][1]:
                    quantiles[q] = (lower if math.isinf(upper) else lower + (upper-lower)*(q*buckets[-1][1]-previous)/(count-previous))*1000
                    break
                lower, previous = upper, count
        result['server'][metric] = quantiles
    result['server'].update({
        'ticks_per_second': delta['game_ticks_total']/seconds,
        'egress_MB_per_second': delta['game_bytes_sent_total']/seconds/1e6,
        'cpu_cores': delta['process_cpu_seconds_total']/seconds,
        'shed': delta['game_broadcasts_shed_total'],
        'write_errors': delta['game_ws_write_errors_total'],
        'drops': delta['game_broadcasts_dropped_total'],
    })
    result['client'] = clients
    (root/'summary.json').write_text(json.dumps(result, indent=2)+'\n')
    print(json.dumps(result, indent=2))
