import 'dart:async';

import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/database.dart';
import '../../theme/app_theme.dart';
import '../../widgets/page_intro.dart';
import '../../widgets/state_message.dart';
import '../../widgets/status_pill.dart';
import 'database_detail_screen.dart';
import 'database_wizard_screen.dart';
import 'database_widgets.dart';

/// Database Marketplace: the curated engine catalog (deploy one in a few
/// steps) and the databases already deployed from it.
class DatabaseMarketplaceScreen extends StatefulWidget {
  final ApiClient apiClient;
  final bool isAdmin;

  const DatabaseMarketplaceScreen({
    super.key,
    required this.apiClient,
    required this.isAdmin,
  });

  @override
  State<DatabaseMarketplaceScreen> createState() =>
      _DatabaseMarketplaceScreenState();
}

class _DatabaseMarketplaceScreenState extends State<DatabaseMarketplaceScreen>
    with SingleTickerProviderStateMixin {
  late final TabController _tabs = TabController(length: 2, vsync: this);
  late Future<List<DatabaseEngine>> _enginesFuture;
  late Future<List<DatabaseInstance>> _databasesFuture;
  final _search = TextEditingController();
  String? _category;
  Timer? _poll;

  @override
  void initState() {
    super.initState();
    _enginesFuture = widget.apiClient.listDatabaseEngines();
    _databasesFuture = widget.apiClient.listDatabases();
    // Deployments move through pulling/creating/verifying; keep the list
    // current without a manual refresh.
    _poll = Timer.periodic(kListPollInterval, (_) {
      if (mounted && _tabs.index == 1) _refreshDatabases();
    });
  }

  @override
  void dispose() {
    _poll?.cancel();
    _tabs.dispose();
    _search.dispose();
    super.dispose();
  }

  void _refreshDatabases() {
    setState(() {
      _databasesFuture = widget.apiClient.listDatabases();
    });
  }

  Future<void> _openWizard(DatabaseEngine engine) async {
    final created = await Navigator.of(context).push<DatabaseInstance>(
      MaterialPageRoute(
        builder: (_) =>
            DatabaseWizardScreen(apiClient: widget.apiClient, engine: engine),
      ),
    );
    if (created == null || !mounted) return;
    _refreshDatabases();
    _tabs.animateTo(1);
    await _openDetail(created.id);
  }

  Future<void> _openDetail(String id) async {
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => DatabaseDetailScreen(
          apiClient: widget.apiClient,
          databaseId: id,
          isAdmin: widget.isAdmin,
        ),
      ),
    );
    if (mounted) _refreshDatabases();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Databases'),
        bottom: TabBar(
          controller: _tabs,
          tabs: const [
            Tab(text: 'Marketplace'),
            Tab(text: 'My databases'),
          ],
        ),
      ),
      body: TabBarView(
        controller: _tabs,
        children: [_buildCatalog(context), _buildInstances(context)],
      ),
    );
  }

  Widget _buildCatalog(BuildContext context) {
    return FutureBuilder<List<DatabaseEngine>>(
      future: _enginesFuture,
      builder: (context, snapshot) {
        if (snapshot.connectionState == ConnectionState.waiting) {
          return const Center(child: CircularProgressIndicator());
        }
        if (snapshot.hasError) {
          return StateMessage.error(
            what: 'the database catalog',
            error: snapshot.error,
            onRetry: () => setState(() {
              _enginesFuture = widget.apiClient.listDatabaseEngines();
            }),
          );
        }
        final all = snapshot.data ?? [];
        final query = _search.text.trim().toLowerCase();
        final engines = all.where((e) {
          if (_category != null && e.category != _category) return false;
          if (query.isEmpty) return true;
          return e.name.toLowerCase().contains(query) ||
              e.description.toLowerCase().contains(query);
        }).toList();

        return CustomScrollView(
          slivers: [
            const SliverToBoxAdapter(
              child: PageIntro(
                description:
                    'Curated, production-ready database templates: '
                    'persistent storage, health checks, resource limits, '
                    'loopback-only ports by default, and generated '
                    'credentials kept in the encrypted vault.',
              ),
            ),
            SliverToBoxAdapter(
              child: Padding(
                padding: const EdgeInsets.fromLTRB(
                  Space.lg,
                  Space.md,
                  Space.lg,
                  0,
                ),
                child: TextField(
                  controller: _search,
                  onChanged: (_) => setState(() {}),
                  decoration: const InputDecoration(
                    hintText: 'Search databases',
                    prefixIcon: Icon(Icons.search),
                    isDense: true,
                    border: OutlineInputBorder(),
                  ),
                ),
              ),
            ),
            SliverToBoxAdapter(
              child: Padding(
                padding: const EdgeInsets.symmetric(
                  horizontal: Space.lg,
                  vertical: Space.md,
                ),
                child: Wrap(
                  spacing: Space.sm,
                  runSpacing: Space.sm,
                  children: [
                    ChoiceChip(
                      label: Text('All (${all.length})'),
                      selected: _category == null,
                      onSelected: (_) => setState(() => _category = null),
                    ),
                    for (final c in databaseCategories.entries)
                      ChoiceChip(
                        label: Text(
                          '${c.value} (${all.where((e) => e.category == c.key).length})',
                        ),
                        selected: _category == c.key,
                        onSelected: (_) => setState(() => _category = c.key),
                      ),
                  ],
                ),
              ),
            ),
            if (engines.isEmpty)
              // hasScrollBody: StateMessage lays out with a LayoutBuilder,
              // which can't report the intrinsic size a non-scrolling
              // SliverFillRemaining asks for.
              const SliverFillRemaining(
                child: StateMessage(
                  icon: Icons.search_off,
                  title: 'No matching databases',
                  message: 'Try a different search or category.',
                ),
              )
            else
              SliverPadding(
                padding: const EdgeInsets.fromLTRB(
                  Space.lg,
                  0,
                  Space.lg,
                  Space.xl,
                ),
                sliver: SliverGrid(
                  gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
                    maxCrossAxisExtent: 420,
                    mainAxisExtent: 212,
                    crossAxisSpacing: Space.md,
                    mainAxisSpacing: Space.md,
                  ),
                  delegate: SliverChildBuilderDelegate(
                    (context, i) => _EngineCard(
                      engine: engines[i],
                      onDeploy: () => _openWizard(engines[i]),
                    ),
                    childCount: engines.length,
                  ),
                ),
              ),
          ],
        );
      },
    );
  }

  Widget _buildInstances(BuildContext context) {
    return FutureBuilder<List<DatabaseInstance>>(
      future: _databasesFuture,
      builder: (context, snapshot) {
        if (snapshot.connectionState == ConnectionState.waiting &&
            !snapshot.hasData) {
          return const Center(child: CircularProgressIndicator());
        }
        if (snapshot.hasError) {
          return StateMessage.error(
            what: 'databases',
            error: snapshot.error,
            onRetry: _refreshDatabases,
          );
        }
        final list = snapshot.data ?? [];
        if (list.isEmpty) {
          return StateMessage(
            icon: Icons.storage_outlined,
            title: 'No databases yet',
            message:
                'Pick an engine from the marketplace to deploy your first '
                'database.',
            actionLabel: 'Browse marketplace',
            actionIcon: Icons.storefront_outlined,
            onAction: () => _tabs.animateTo(0),
          );
        }
        final healthy = list
            .where((d) => databaseStatus(d).tone == StatusTone.healthy)
            .length;
        final failing = list
            .where((d) => databaseStatus(d).tone == StatusTone.failed)
            .length;
        return RefreshIndicator(
          onRefresh: () async => _refreshDatabases(),
          child: ListView(
            padding: const EdgeInsets.only(bottom: Space.xl),
            children: [
              PageIntro(
                description:
                    'Databases deployed from the marketplace. Open one for '
                    'its connection details, credentials and backups.',
                summary: [
                  SummaryStat(value: '${list.length}', label: 'databases'),
                  SummaryStat(
                    value: '$healthy',
                    label: 'healthy',
                    color: AppColors.healthy,
                  ),
                  if (failing > 0)
                    SummaryStat(
                      value: '$failing',
                      label: 'need attention',
                      color: AppColors.failed,
                    ),
                ],
              ),
              const SizedBox(height: Space.md),
              for (final d in list)
                Padding(
                  padding: const EdgeInsets.symmetric(
                    horizontal: Space.lg,
                    vertical: Space.xs,
                  ),
                  child: _InstanceTile(
                    instance: d,
                    onTap: () => _openDetail(d.id),
                  ),
                ),
            ],
          ),
        );
      },
    );
  }
}

class _EngineCard extends StatelessWidget {
  final DatabaseEngine engine;
  final VoidCallback onDeploy;

  const _EngineCard({required this.engine, required this.onDeploy});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      margin: EdgeInsets.zero,
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        onTap: onDeploy,
        child: Padding(
          padding: const EdgeInsets.all(Space.lg),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  EngineAvatar(category: engine.category),
                  const SizedBox(width: Space.md),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          engine.name,
                          style: theme.textTheme.titleMedium,
                          overflow: TextOverflow.ellipsis,
                        ),
                        Text(
                          '${databaseCategories[engine.category] ?? engine.category}'
                          ' · ${engine.versions.first.label}',
                          style: theme.textTheme.bodySmall,
                        ),
                      ],
                    ),
                  ),
                ],
              ),
              const SizedBox(height: Space.sm),
              Expanded(
                child: Text(
                  engine.description,
                  style: theme.textTheme.bodySmall?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                  maxLines: 3,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              Wrap(
                spacing: Space.xs,
                runSpacing: Space.xs,
                children: [
                  EngineBadge(
                    icon: engine.hasCredentials
                        ? Icons.lock_outline
                        : Icons.lock_open,
                    label: switch (engine.auth) {
                      'password' => 'Password auth',
                      'token' => 'API key',
                      _ => 'No auth',
                    },
                    warning: !engine.hasCredentials,
                  ),
                  if (engine.supportsHighAvailability)
                    const EngineBadge(
                      icon: Icons.copy_all_outlined,
                      label: 'HA option',
                    ),
                  if (engine.architectures.length == 1)
                    EngineBadge(
                      icon: Icons.memory,
                      label: '${engine.architectures.first} only',
                      warning: true,
                    ),
                  if (!engine.persistent)
                    const EngineBadge(
                      icon: Icons.bolt_outlined,
                      label: 'In-memory',
                    ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _InstanceTile extends StatelessWidget {
  final DatabaseInstance instance;
  final VoidCallback onTap;

  const _InstanceTile({required this.instance, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final status = databaseStatus(instance);
    final d = instance;
    return Card(
      margin: EdgeInsets.zero,
      child: ListTile(
        onTap: onTap,
        leading: EngineAvatar(category: d.category),
        title: Row(
          children: [
            Flexible(child: Text(d.name, overflow: TextOverflow.ellipsis)),
            const SizedBox(width: Space.sm),
            StatusPill(label: status.label, tone: status.tone),
          ],
        ),
        subtitle: Text(
          '${d.engineName} ${d.version} · ${d.serverName} · '
          '${d.access == 'local' ? '127.0.0.1' : d.host}:${d.port}'
          '${d.profile == 'production' ? ' · production' : ''}'
          '${d.backupCron != null ? ' · scheduled backups' : ''}',
          style: theme.textTheme.bodySmall,
        ),
        trailing: const Icon(Icons.chevron_right),
      ),
    );
  }
}
