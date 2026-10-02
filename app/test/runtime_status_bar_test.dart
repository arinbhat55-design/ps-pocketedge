import 'package:app/api/api_client.dart';
import 'package:app/features/shell/runtime_status_bar.dart';
import 'package:app/models/server.dart';
import 'package:app/models/server_metrics.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

const gib = 1024 * 1024 * 1024;

Server host(String id, {bool online = true, ResourceSnapshot? resources}) =>
    Server(
      id: id,
      name: id,
      hostname: id,
      os: 'linux',
      arch: 'amd64',
      agentVersion: 'test',
      status: 'online',
      createdAt: DateTime(2026),
      lastHeartbeatAt: DateTime.now().subtract(
        online ? Duration.zero : const Duration(minutes: 5),
      ),
      lastResources:
          resources ??
          const ResourceSnapshot(
            cpuPercent: 25,
            memPercent: 50,
            diskPercent: 40,
            usedMemoryBytes: 4 * gib,
            totalMemoryBytes: 8 * gib,
            usedDiskBytes: 40 * gib,
            totalDiskBytes: 100 * gib,
          ),
    );

class MetricsClient extends ApiClient {
  List<Server> servers;
  bool fail = false;
  int calls = 0;
  List<ContainerInfo> containers = [];
  String? requestedServer;

  MetricsClient(this.servers) : super(baseUrl: 'http://localhost');

  @override
  Future<List<Server>> listServers() async {
    calls++;
    if (fail) throw Exception('offline');
    return servers;
  }

  @override
  Future<ServerDetail> getServerDetail(String serverId) async {
    requestedServer = serverId;
    return ServerDetail(
      server: servers.firstWhere((s) => s.id == serverId),
      containers: containers,
    );
  }
}

void main() {
  Future<void> pumpBar(
    WidgetTester tester,
    MetricsClient client, {
    Size size = const Size(1280, 900),
    bool admin = true,
    ValueChanged<Server>? open,
  }) async {
    tester.view.physicalSize = size;
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: const SizedBox(),
          bottomNavigationBar: RuntimeStatusBar(
            apiClient: client,
            isAdmin: admin,
            onOpenTerminal: open ?? (_) {},
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  testWidgets('shows exact host RAM and disk usage and CPU', (tester) async {
    await pumpBar(tester, MetricsClient([host('Local')]));
    expect(
      tester.getSize(find.byKey(const ValueKey('runtime-status-bar'))).height,
      32,
    );
    expect(find.byType(LinearProgressIndicator), findsNothing);
    expect(find.text('CPU 25.0%'), findsOneWidget);
    expect(find.text('RAM 4 GB / 8 GB'), findsOneWidget);
    expect(find.text('Disk 40 GB / 100 GB limit'), findsOneWidget);
    expect(find.text('Local · Online'), findsOneWidget);
  });

  testWidgets('switches host metrics and preserves selection across polling', (
    tester,
  ) async {
    final client = MetricsClient([
      host('Local'),
      host(
        'Remote',
        resources: const ResourceSnapshot(
          cpuPercent: 75,
          memPercent: 20,
          diskPercent: 30,
          usedMemoryBytes: 2 * gib,
          totalMemoryBytes: 10 * gib,
          usedDiskBytes: 30 * gib,
          totalDiskBytes: 100 * gib,
        ),
      ),
    ]);
    await pumpBar(tester, client);
    await tester.tap(find.byTooltip('Select metrics server'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Remote · Online'));
    await tester.pumpAndSettle();
    expect(find.text('CPU 75.0%'), findsOneWidget);
    await tester.pump(const Duration(seconds: 10));
    await tester.pumpAndSettle();
    expect(client.calls, 2);
    expect(find.text('RAM 2 GB / 10 GB'), findsOneWidget);
    client.servers = [host('Local')];
    await tester.pump(const Duration(seconds: 10));
    await tester.pumpAndSettle();
    expect(find.text('Local · Online'), findsOneWidget);
  });

  testWidgets('offline readings are labelled and terminals disabled', (
    tester,
  ) async {
    await pumpBar(tester, MetricsClient([host('Offline', online: false)]));
    expect(find.text('Offline · Disconnected'), findsOneWidget);
    expect(find.text('Last reported'), findsOneWidget);
    expect(
      tester
          .widget<TextButton>(find.byKey(const ValueKey('status-bar-terminal')))
          .onPressed,
      isNull,
    );
  });

  testWidgets('viewer cannot launch a terminal', (tester) async {
    await pumpBar(tester, MetricsClient([host('Local')]), admin: false);
    expect(
      find.byTooltip('Administrator access required for terminals'),
      findsOneWidget,
    );
    expect(
      tester
          .widget<TextButton>(find.byKey(const ValueKey('status-bar-terminal')))
          .onPressed,
      isNull,
    );
  });

  testWidgets(
    'old agents show percentages and unknown capacity, not invented bytes',
    (tester) async {
      await pumpBar(
        tester,
        MetricsClient([
          host(
            'Old',
            resources: ResourceSnapshot.fromJson({
              'cpuPercent': 10,
              'memPercent': 50,
              'diskPercent': 40,
            }),
          ),
        ]),
      );
      expect(find.text('RAM 50.0% / —'), findsOneWidget);
      expect(find.text('Disk 40.0% / — limit'), findsOneWidget);
    },
  );

  testWidgets('empty fleet and failed requests remain usable', (tester) async {
    final client = MetricsClient([]);
    await pumpBar(tester, client);
    expect(find.text('No servers connected'), findsOneWidget);
    client.fail = true;
    await tester.pump(const Duration(seconds: 10));
    await tester.pumpAndSettle();
    expect(find.text('Metrics unavailable'), findsOneWidget);
    client.fail = false;
    client.servers = [host('Local')];
    await tester.tap(find.byTooltip('Retry metrics'));
    await tester.pumpAndSettle();
    expect(find.text('Local · Online'), findsOneWidget);
  });

  testWidgets('terminal opens selected host without requiring containers', (
    tester,
  ) async {
    final client = MetricsClient([host('Local'), host('Remote')]);
    Server? selectedServer;
    await pumpBar(tester, client, open: (server) => selectedServer = server);
    await tester.tap(find.byTooltip('Select metrics server'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Remote · Online'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('status-bar-terminal')));
    await tester.pumpAndSettle();
    expect(selectedServer?.id, 'Remote');
    expect(client.requestedServer, isNull);
    expect(find.byType(AlertDialog), findsNothing);
  });

  testWidgets('phone layouts scroll metrics without overflowing', (
    tester,
  ) async {
    await pumpBar(
      tester,
      MetricsClient([host('Local')]),
      size: const Size(320, 640),
    );
    expect(
      tester.getSize(find.byKey(const ValueKey('runtime-status-bar'))).height,
      32,
    );
    expect(find.text('Terminal').hitTestable(), findsOneWidget);
    await tester.drag(
      find.byType(SingleChildScrollView),
      const Offset(-500, 0),
    );
    await tester.pumpAndSettle();
    expect(
      find.text('Disk 40 GB / 100 GB limit').hitTestable(),
      findsOneWidget,
    );
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    await tester.pump(const Duration(seconds: 10));
    expect(tester.takeException(), isNull);
  });
}
