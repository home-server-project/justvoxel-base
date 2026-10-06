<?php
// First-run adapter to upstream Artisan commands, not a container management API.
// Input comes through stdin. Never print credentials or upstream exception details.
require '/app/vendor/autoload.php';
$app = require '/app/bootstrap/app.php';
$kernel = $app->make(Illuminate\Contracts\Console\Kernel::class);
$kernel->bootstrap();
try {
    $input = json_decode(stream_get_contents(STDIN), true, 32, JSON_THROW_ON_ERROR);
    if ($input['action'] === 'verify') {
        $node = Pterodactyl\Models\Node::findOrFail($input['node_id']);
        $app->make(Pterodactyl\Repositories\Wings\DaemonConfigurationRepository::class)
            ->setNode($node)->getSystemInformation();
        echo json_encode(['ok' => true]);
        exit(0);
    }
    $user = Pterodactyl\Models\User::where('username', $input['username'])->first();
    if (!$user) {
        $kernel->call('p:user:make', [
            '--email' => $input['email'], '--username' => $input['username'],
            '--name-first' => $input['username'], '--name-last' => 'Administrator',
            '--password' => $input['password'], '--admin' => 1, '--no-interaction' => true,
        ]);
        $user = Pterodactyl\Models\User::where('username', $input['username'])->firstOrFail();
    }
    if (!$user->root_admin || $user->email !== $input['email']) {
        throw new RuntimeException('Conflicting administrator');
    }
    if (!$input['wings']) {
        echo json_encode(['ok' => true]);
        exit(0);
    }
    $location = Pterodactyl\Models\Location::where('short', 'justvoxel-plus')->first();
    if (!$location) {
        $kernel->call('p:location:make', ['--short' => 'justvoxel-plus', '--long' => 'JustVoxel Plus host', '--no-interaction' => true]);
        $location = Pterodactyl\Models\Location::where('short', 'justvoxel-plus')->firstOrFail();
    }
    $node = Pterodactyl\Models\Node::where('name', 'JustVoxel Plus')->first();
    if (!$node) {
        $kernel->call('p:node:make', [
            '--name' => 'JustVoxel Plus', '--description' => 'Local appliance node',
            '--locationId' => $location->id, '--fqdn' => $input['host'],
            '--scheme' => $input['scheme'], '--public' => 1, '--proxy' => 0,
            '--maintenance' => 0, '--maxMemory' => $input['memory'], '--overallocateMemory' => 0,
            '--maxDisk' => $input['disk'], '--overallocateDisk' => 0, '--uploadSize' => 100,
            '--daemonListeningPort' => 8080, '--daemonSFTPPort' => 2022,
            '--daemonBase' => $input['data_root'] . '/wings/data', '--no-interaction' => true,
        ]);
        $node = Pterodactyl\Models\Node::where('name', 'JustVoxel Plus')->firstOrFail();
    }
    if ($node->fqdn !== $input['host'] || $node->daemonBase !== $input['data_root'] . '/wings/data') {
        throw new RuntimeException('Conflicting node');
    }
    echo json_encode(['ok' => true, 'node_id' => $node->id, 'configuration' => $node->getConfiguration()]);
} catch (Throwable $exception) {
    fwrite(STDERR, "Upstream Panel setup command failed.\n");
    exit(1);
}
