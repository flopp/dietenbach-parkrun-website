<?php
header('Content-Type: application/json; charset=utf-8');
header('Cache-Control: no-store, no-cache, must-revalidate');

$now = new DateTimeImmutable('now', new DateTimeZone('Europe/Berlin'));
$today = $now->format('Y-m-d');
$firstDay = $now->modify('-6 days')->format('Y-m-d');
$periods = [
    'day' => $today,
];

$counter = fopen(__DIR__ . '/visitor-count.txt', 'c+');
if ($counter === false) {
    error_log('Unable to open visitor counter file.');
    http_response_code(500);
    echo json_encode(['error' => 'Counter unavailable']);
    exit;
}

if (!flock($counter, LOCK_EX)) {
    error_log('Unable to lock visitor counter file.');
    fclose($counter);
    http_response_code(500);
    echo json_encode(['error' => 'Counter unavailable']);
    exit;
}

$contents = stream_get_contents($counter);
$value = trim($contents === false ? '' : $contents);
if ($contents === false) {
    error_log('Visitor counter file contains invalid data.');
    flock($counter, LOCK_UN);
    fclose($counter);
    http_response_code(500);
    echo json_encode(['error' => 'Counter unavailable']);
    exit;
}

if ($value === '') {
    $state = ['total' => 0, 'periods' => []];
} elseif (ctype_digit($value)) {
    $state = ['total' => (int) $value, 'periods' => []];
} else {
    $state = json_decode($value, true);
    if (
        !is_array($state)
        || !isset($state['total'], $state['periods'])
        || !is_int($state['total'])
        || $state['total'] < 0
        || !is_array($state['periods'])
    ) {
        error_log('Visitor counter file contains invalid data.');
        flock($counter, LOCK_UN);
        fclose($counter);
        http_response_code(500);
        echo json_encode(['error' => 'Counter unavailable']);
        exit;
    }
}

unset($state['periods']['month'], $state['periods']['year']);

$counts = [];
foreach ($periods as $period => $key) {
    if (!array_key_exists($period, $state['periods'])) {
        $current = ['key' => $key, 'count' => 0];
    } else {
        $current = $state['periods'][$period];
        if (
            !is_array($current)
            || !isset($current['key'], $current['count'])
            || !is_string($current['key'])
            || !is_int($current['count'])
            || $current['count'] < 0
        ) {
            error_log('Visitor counter file contains invalid period data.');
            flock($counter, LOCK_UN);
            fclose($counter);
            http_response_code(500);
            echo json_encode(['error' => 'Counter unavailable']);
            exit;
        }
    }

    if ($current['key'] !== $key) {
        $current['count'] = 0;
    }

    if ($current['count'] === PHP_INT_MAX) {
        error_log('Visitor period count has reached the maximum value.');
        flock($counter, LOCK_UN);
        fclose($counter);
        http_response_code(500);
        echo json_encode(['error' => 'Counter unavailable']);
        exit;
    }

    $counts[$period] = $current['count'] + 1;
    $state['periods'][$period] = ['key' => $key, 'count' => $counts[$period]];
}

if (!array_key_exists('days', $state)) {
    $state['days'] = [];
} elseif (!is_array($state['days'])) {
    error_log('Visitor counter file contains invalid daily data.');
    flock($counter, LOCK_UN);
    fclose($counter);
    http_response_code(500);
    echo json_encode(['error' => 'Counter unavailable']);
    exit;
}

$days = $state['days'];
foreach ($days as $day => $count) {
    $date = is_string($day) && preg_match('/^\d{4}-\d{2}-\d{2}$/D', $day) === 1
        ? DateTimeImmutable::createFromFormat('!Y-m-d', $day, $now->getTimezone())
        : false;
    if (
        $date === false
        || $date->format('Y-m-d') !== $day
        || !is_int($count)
        || $count < 0
    ) {
        error_log('Visitor counter file contains invalid daily data.');
        flock($counter, LOCK_UN);
        fclose($counter);
        http_response_code(500);
        echo json_encode(['error' => 'Counter unavailable']);
        exit;
    }

    if ($day < $firstDay || $day > $today) {
        unset($days[$day]);
    }
}
$days[$today] = $counts['day'];

$state['days'] = $days;

$dailyCounts = [];
for ($offset = 0; $offset <= 6; $offset++) {
    $date = $now->modify("-{$offset} days")->format('Y-m-d');
    $dailyCounts[] = [
        'date' => $date,
        'count' => $days[$date] ?? 0,
    ];
}

if ($state['total'] === PHP_INT_MAX) {
    error_log('Visitor total count has reached the maximum value.');
    flock($counter, LOCK_UN);
    fclose($counter);
    http_response_code(500);
    echo json_encode(['error' => 'Counter unavailable']);
    exit;
}
$state['total']++;
$serialized = json_encode($state);
if ($serialized === false) {
    error_log('Unable to encode visitor counter data.');
    flock($counter, LOCK_UN);
    fclose($counter);
    http_response_code(500);
    echo json_encode(['error' => 'Counter unavailable']);
    exit;
}

if (!ftruncate($counter, 0) || !rewind($counter)) {
    error_log('Unable to update visitor counter file.');
    flock($counter, LOCK_UN);
    fclose($counter);
    http_response_code(500);
    echo json_encode(['error' => 'Counter unavailable']);
    exit;
}

if (fwrite($counter, $serialized) !== strlen($serialized) || !fflush($counter)) {
    error_log('Unable to write visitor counter file.');
    flock($counter, LOCK_UN);
    fclose($counter);
    http_response_code(500);
    echo json_encode(['error' => 'Counter unavailable']);
    exit;
}

flock($counter, LOCK_UN);
fclose($counter);
echo json_encode([
    'daily' => $counts['day'],
    'dailyCounts' => $dailyCounts,
    'total' => $state['total'],
]);
