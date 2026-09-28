import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../api/api_client.dart';
import '../../models/database.dart';
import '../../models/image.dart' show formatBytes;
import '../../theme/app_theme.dart';
import 'query_download.dart';

/// PostgreSQL's live administration and diagnostic tools. Mutations are
/// available only to the instance owner or an administrator (also checked by
/// the API); the SQL console runs with the instance's administrator role.
class PostgresAdminScreen extends StatefulWidget {
  final ApiClient api;
  final DatabaseInstance database;
  final bool canManage;

  const PostgresAdminScreen({
    super.key,
    required this.api,
    required this.database,
    required this.canManage,
  });

  @override
  State<PostgresAdminScreen> createState() => _PostgresAdminScreenState();
}

class _PostgresAdminScreenState extends State<PostgresAdminScreen> {
  late Future<Map<String, dynamic>> _overview;
  late Future<Map<String, dynamic>> _metrics;
  late Future<List<Map<String, dynamic>>> _alerts;
  late Future<List<Map<String, dynamic>>> _sessions;
  late Future<List<Map<String, dynamic>>> _slowQueries;
  late Future<Map<String, dynamic>> _sizes;
  final _sql = TextEditingController(text: 'SELECT current_database(), now();');
  final _queryDatabase = TextEditingController();
  String? _csv;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _refresh();
  }

  @override
  void dispose() {
    _sql.dispose();
    _queryDatabase.dispose();
    super.dispose();
  }

  void _refresh() {
    setState(() {
      _overview = widget.api.postgresOverview(widget.database.id);
      _metrics = widget.api.postgresMetrics(widget.database.id);
      _alerts = widget.api.postgresAlerts(widget.database.id);
      _sessions = widget.canManage
          ? widget.api.postgresSessions(widget.database.id)
          : Future.value([]);
      _slowQueries = widget.canManage
          ? widget.api.postgresSlowQueries(widget.database.id)
          : Future.value([]);
      _sizes = widget.api.postgresSizes(widget.database.id);
    });
    // Tabs that aren't built yet have no listener on their future; mark
    // failures handled so they surface in the tab instead of as uncaught
    // async errors. FutureBuilder still receives the error.
    for (final f in <Future<Object?>>[
      _overview,
      _metrics,
      _alerts,
      _sessions,
      _slowQueries,
      _sizes,
    ]) {
      f.ignore();
    }
  }

  void _message(String value) {
    if (mounted) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text(value)));
    }
  }

  Future<void> _run(Future<void> Function() action) async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await action();
      if (mounted) _refresh();
    } catch (e) {
      _message(e is ApiException ? e.message : '$e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<String?> _ask(
    String title,
    String label, {
    String initial = '',
  }) async {
    final controller = TextEditingController(text: initial);
    final result = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(title),
        content: TextField(
          controller: controller,
          autofocus: true,
          decoration: InputDecoration(
            labelText: label,
            border: const OutlineInputBorder(),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, controller.text.trim()),
            child: const Text('Continue'),
          ),
        ],
      ),
    );
    controller.dispose();
    return result?.isEmpty == true ? null : result;
  }

  Future<bool> _confirm(String title, String message) async =>
      await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: Text(title),
          content: Text(message),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('Confirm'),
            ),
          ],
        ),
      ) ??
      false;

  @override
  Widget build(BuildContext context) => DefaultTabController(
    length: 4,
    child: Scaffold(
      appBar: AppBar(
        title: Text('${widget.database.name} · PostgreSQL'),
        actions: [
          IconButton(
            onPressed: _refresh,
            icon: const Icon(Icons.refresh),
            tooltip: 'Refresh',
          ),
        ],
        bottom: const TabBar(
          isScrollable: true,
          tabs: [
            Tab(text: 'Monitoring'),
            Tab(text: 'Sessions'),
            Tab(text: 'Storage'),
            Tab(text: 'SQL & admin'),
          ],
        ),
      ),
      body: TabBarView(
        children: [_monitoring(), _sessionList(), _storage(), _admin()],
      ),
    ),
  );

  Widget _future<T>(
    Future<T> future,
    Widget Function(T) builder, {
    Widget Function(ApiException)? onConflict,
  }) => FutureBuilder<T>(
    future: future,
    builder: (context, snapshot) {
      final error = snapshot.error;
      if (error is ApiException &&
          error.statusCode == 409 &&
          onConflict != null) {
        return onConflict(error);
      }
      if (error != null) {
        final reason = error is ApiException ? error.message : '$error';
        return Center(child: Text('Could not load: $reason'));
      }
      if (!snapshot.hasData) {
        return const Center(child: CircularProgressIndicator());
      }
      return builder(snapshot.data as T);
    },
  );

  Widget _monitoring() => _future(
    _overview,
    (m) => ListView(
      padding: const EdgeInsets.all(Space.lg),
      children: [
        Text(
          'Server ${m['serverVersion']}',
          style: Theme.of(context).textTheme.titleMedium,
        ),
        const SizedBox(height: Space.md),
        _metric(
          'Connections',
          '${m['connectionCount']} / ${m['connectionLimit'] == -1 ? 'unlimited' : m['connectionLimit']}',
        ),
        _metric(
          'Transactions since statistics reset',
          '${m['transactionsTotal']}',
        ),
        _future(
          _metrics,
          (history) => Column(
            children: [
              _metric(
                'Transaction rate',
                '${(history['transactionRate'] as num).toStringAsFixed(2)} / s',
              ),
              _metric(
                'Storage growth since last sample',
                formatBytes((history['storageGrowthBytes'] as num).toInt()),
              ),
            ],
          ),
        ),
        _metric(
          'Cache hit ratio',
          '${((m['cacheHitRatio'] as num? ?? 0) * 100).toStringAsFixed(1)}%',
        ),
        _metric('Waiting on locks', '${m['lockWaitCount']}'),
        _metric('Deadlocks since statistics reset', '${m['deadlocksTotal']}'),
        _metric('Active queries', '${m['activeQueryCount']}'),
        _metric(
          'Average active query elapsed',
          m['activeQueryLatencyMs'] == null
              ? 'No active queries'
              : '${m['activeQueryLatencyMs']} ms',
        ),
        _metric('Queries over 30 seconds', '${m['slowQueryCount']}'),
        _metric(
          'Transactions over 5 minutes',
          '${m['longRunningTransactionCount']}',
        ),
        _metric(
          'Database size',
          formatBytes((m['databaseBytes'] as num? ?? 0).toInt()),
        ),
        _metric(
          'Replication replay lag',
          m['replicationLagSeconds'] == null &&
                  m['standbyReplayLagSeconds'] == null
              ? 'No replica reporting'
              : '${m['replicationLagSeconds'] ?? m['standbyReplayLagSeconds']} s',
        ),
        _future(
          _alerts,
          (alerts) => Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const SizedBox(height: Space.md),
              Text(
                'Active alerts',
                style: Theme.of(context).textTheme.titleMedium,
              ),
              if (alerts.isEmpty)
                const ListTile(title: Text('No active alerts')),
              for (final alert in alerts)
                Card(
                  child: ListTile(
                    leading: const Icon(Icons.warning_amber_outlined),
                    title: Text('${alert['kind']}'),
                    subtitle: Text('${alert['message']}'),
                  ),
                ),
            ],
          ),
        ),
        const SizedBox(height: Space.md),
        OutlinedButton.icon(
          onPressed: _busy
              ? null
              : () => _run(() async {
                  final result = await widget.api.testPostgresConnection(
                    widget.database.id,
                  );
                  _message(
                    'Connected to ${result['database']} (${result['latencyMs']} ms)',
                  );
                }),
          icon: const Icon(Icons.cable),
          label: const Text('Test connection'),
        ),
      ],
    ),
  );

  Widget _metric(String name, String value) => Card(
    child: ListTile(title: Text(name), trailing: Text(value)),
  );

  Widget _sessionList() => !widget.canManage
      ? const Center(
          child: Text(
            'Session and query details are available to the database owner and administrators.',
          ),
        )
      : _future(
          _sessions,
          (rows) => ListView(
            padding: const EdgeInsets.all(Space.lg),
            children: [
              Text(
                '${rows.length} sessions',
                style: Theme.of(context).textTheme.titleMedium,
              ),
              const SizedBox(height: Space.sm),
              if (rows.isEmpty) const Text('No client sessions.'),
              for (final row in rows)
                Card(
                  child: ListTile(
                    title: Text(
                      'PID ${row['pid']} · ${row['user']} · ${row['state']}',
                    ),
                    subtitle: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          '${row['durationSeconds']} s · ${row['waitEventType'] ?? 'no wait'}',
                        ),
                        SelectableText('${row['query'] ?? ''}', maxLines: 4),
                      ],
                    ),
                    trailing: widget.canManage
                        ? IconButton(
                            icon: const Icon(Icons.cancel_outlined),
                            tooltip: 'Terminate session',
                            onPressed: _busy
                                ? null
                                : () async {
                                    if (!await _confirm(
                                      'Terminate session?',
                                      'PID ${row['pid']} will be disconnected.',
                                    )) {
                                      return;
                                    }
                                    await _run(
                                      () => widget.api.postgresTerminateSession(
                                        widget.database.id,
                                        row['pid'] as int,
                                      ),
                                    );
                                  },
                          )
                        : null,
                  ),
                ),
              const SizedBox(height: Space.lg),
              Text(
                'Slow query history',
                style: Theme.of(context).textTheme.titleMedium,
              ),
              const SizedBox(height: Space.sm),
              _future(
                _slowQueries,
                (queries) => Column(
                  children: [
                    if (queries.isEmpty)
                      const ListTile(title: Text('No queries recorded yet.')),
                    for (final query in queries)
                      Card(
                        child: ListTile(
                          title: Text(
                            '${query['meanMs']} ms average · ${query['calls']} calls',
                          ),
                          subtitle: SelectableText(
                            '${query['query']}',
                            maxLines: 4,
                          ),
                          trailing: Text('${query['maxMs']} ms max'),
                        ),
                      ),
                  ],
                ),
                // Insights not enabled yet: explain, and let the button
                // below turn them on.
                onConflict: (e) => ListTile(
                  leading: const Icon(Icons.info_outline),
                  title: Text(e.message),
                ),
              ),
              if (widget.canManage)
                OutlinedButton.icon(
                  onPressed: _busy
                      ? null
                      : () => _run(() async {
                          await widget.api.enablePostgresInsights(
                            widget.database.id,
                          );
                          _message('Query insights enabled');
                        }),
                  icon: const Icon(Icons.insights_outlined),
                  label: const Text('Enable query insights'),
                ),
            ],
          ),
        );

  Widget _storage() => _future(_sizes, (m) {
    final databases = (m['databases'] as List<dynamic>? ?? [])
        .cast<Map<String, dynamic>>();
    final tables = (m['tables'] as List<dynamic>? ?? [])
        .cast<Map<String, dynamic>>();
    return ListView(
      padding: const EdgeInsets.all(Space.lg),
      children: [
        Text('Databases', style: Theme.of(context).textTheme.titleMedium),
        for (final db in databases)
          ListTile(
            title: Text('${db['name']}'),
            subtitle: Text(formatBytes((db['bytes'] as num).toInt())),
            trailing:
                widget.canManage &&
                    db['name'] != widget.database.databaseName &&
                    db['name'] != 'postgres' &&
                    !'${db['name']}'.startsWith('template')
                ? IconButton(
                    icon: const Icon(Icons.delete_outline),
                    tooltip: 'Delete database',
                    onPressed: _busy
                        ? null
                        : () async {
                            if (!await _confirm(
                              'Delete ${db['name']}?',
                              'This permanently deletes the logical database and disconnects its sessions.',
                            )) {
                              return;
                            }
                            await _run(
                              () => widget.api.postgresDeleteDatabase(
                                widget.database.id,
                                '${db['name']}',
                              ),
                            );
                          },
                  )
                : null,
          ),
        if (widget.canManage)
          OutlinedButton.icon(
            onPressed: _busy
                ? null
                : () async {
                    final name = await _ask('Create database', 'Database name');
                    if (name != null) {
                      await _run(
                        () => widget.api.postgresCreateDatabase(
                          widget.database.id,
                          name,
                        ),
                      );
                    }
                  },
            icon: const Icon(Icons.add),
            label: const Text('Create database'),
          ),
        const SizedBox(height: Space.lg),
        Text('Largest tables', style: Theme.of(context).textTheme.titleMedium),
        for (final table in tables)
          ListTile(
            title: Text('${table['schema']}.${table['name']}'),
            subtitle: Text(
              'Table ${formatBytes((table['tableBytes'] as num).toInt())} · indexes ${formatBytes((table['indexBytes'] as num).toInt())}',
            ),
            trailing: Text(formatBytes((table['totalBytes'] as num).toInt())),
          ),
      ],
    );
  });

  Widget _admin() => ListView(
    padding: const EdgeInsets.all(Space.lg),
    children: [
      Text('SQL console', style: Theme.of(context).textTheme.titleMedium),
      const SizedBox(height: Space.sm),
      TextField(
        controller: _queryDatabase,
        decoration: InputDecoration(
          labelText: 'Database',
          hintText: widget.database.databaseName,
          border: const OutlineInputBorder(),
        ),
      ),
      const SizedBox(height: Space.sm),
      TextField(
        controller: _sql,
        minLines: 5,
        maxLines: 12,
        style: const TextStyle(fontFamily: 'monospace'),
        decoration: const InputDecoration(
          border: OutlineInputBorder(),
          hintText: 'Enter SQL',
        ),
      ),
      const SizedBox(height: Space.sm),
      Text(
        'Queries run as the database administrator. Results are limited to 8 MB and 30 seconds.',
        style: Theme.of(context).textTheme.bodySmall,
      ),
      const SizedBox(height: Space.sm),
      FilledButton.icon(
        onPressed: !widget.canManage || _busy
            ? null
            : () => _run(() async {
                final result = await widget.api.postgresQuery(
                  widget.database.id,
                  _sql.text,
                  database: _queryDatabase.text.trim(),
                );
                if (mounted) setState(() => _csv = result);
              }),
        icon: const Icon(Icons.play_arrow),
        label: const Text('Run SQL'),
      ),
      if (_csv != null) ...[
        const SizedBox(height: Space.md),
        Row(
          children: [
            Expanded(
              child: Text(
                'CSV result',
                style: Theme.of(context).textTheme.titleMedium,
              ),
            ),
            IconButton(
              onPressed: () async {
                await Clipboard.setData(ClipboardData(text: _csv!));
                _message('CSV copied');
              },
              icon: const Icon(Icons.copy),
              tooltip: 'Copy CSV',
            ),
          ],
        ),
        OutlinedButton.icon(
          onPressed: () async {
            try {
              final path = await downloadQueryCsv(
                _csv!,
                'query-results-${DateTime.now().millisecondsSinceEpoch}.csv',
              );
              _message('CSV downloaded to $path');
            } catch (e) {
              _message('Could not download CSV: $e');
            }
          },
          icon: const Icon(Icons.download_outlined),
          label: const Text('Download CSV'),
        ),
        SelectableText(_csv!, style: const TextStyle(fontFamily: 'monospace')),
      ],
      if (widget.canManage) ...[
        const Divider(height: Space.xl),
        Text('Administration', style: Theme.of(context).textTheme.titleMedium),
        OutlinedButton.icon(
          onPressed: _busy ? null : _createUser,
          icon: const Icon(Icons.person_add_outlined),
          label: const Text('Create user'),
        ),
        OutlinedButton.icon(
          onPressed: _busy ? null : _setPermission,
          icon: const Icon(Icons.admin_panel_settings_outlined),
          label: const Text('Assign permissions'),
        ),
        OutlinedButton.icon(
          onPressed: _busy ? null : _setConnectionLimit,
          icon: const Icon(Icons.settings_ethernet),
          label: const Text('Set connection limit'),
        ),
      ],
    ],
  );

  Future<void> _createUser() async {
    final username = await _ask('Create user', 'Username');
    if (username == null) return;
    final database = await _ask(
      'User database',
      'Database',
      initial: widget.database.databaseName,
    );
    if (database == null) return;
    final permission = await _permissionChoice();
    if (permission == null) return;
    await _run(() async {
      await widget.api.postgresCreateUser(
        widget.database.id,
        username,
        database,
        permission,
      );
      _message(
        'User created. Reveal its password in the database Credentials section.',
      );
    });
  }

  Future<String?> _permissionChoice() => showDialog<String>(
    context: context,
    builder: (context) => SimpleDialog(
      title: const Text('Permission'),
      children: [
        for (final value in ['read', 'write', 'none'])
          SimpleDialogOption(
            onPressed: () => Navigator.pop(context, value),
            child: Text(value),
          ),
      ],
    ),
  );

  Future<void> _setPermission() async {
    final username = await _ask('Assign permissions', 'Username');
    if (username == null) return;
    final database = await _ask(
      'Target database',
      'Database',
      initial: widget.database.databaseName,
    );
    if (database == null) return;
    final permission = await _permissionChoice();
    if (permission == null) return;
    await _run(
      () => widget.api.postgresSetPermission(
        widget.database.id,
        username,
        database,
        permission,
      ),
    );
  }

  Future<void> _setConnectionLimit() async {
    final database = await _ask(
      'Connection limit',
      'Database',
      initial: widget.database.databaseName,
    );
    if (database == null) return;
    final value = await _ask('Connection limit', '-1 for unlimited');
    if (value == null) return;
    final limit = int.tryParse(value);
    if (limit == null) {
      _message('Enter a valid number');
      return;
    }
    await _run(
      () => widget.api.postgresSetConnectionLimit(
        widget.database.id,
        database,
        limit,
      ),
    );
  }
}
