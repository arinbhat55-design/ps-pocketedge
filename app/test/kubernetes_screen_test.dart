import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:app/api/api_client.dart';
import 'package:app/features/kubernetes/helm_releases_screen.dart';
import 'package:app/features/kubernetes/kubernetes_screen.dart';

const _cluster = {
  'id': 'c1',
  'name': 'lab',
  'apiServer': 'https://k8s.lab:6443',
};

Map<String, dynamic> _overview() => {
  'namespaces': ['default', 'team-a'],
  'nodes': [
    {
      'name': 'node-1',
      'ready': true,
      'cpu': '4',
      'memory': '8Gi',
      'gpu': {'nvidia.com/gpu': '1'},
    },
  ],
  'deployments': [
    {
      'name': 'web',
      'namespace': 'default',
      'ready': 2,
      'replicas': 2,
      'image': 'nginx:1.27',
      'cpu': '250m',
      'memory': '256Mi',
      'gpuResource': 'nvidia.com/gpu',
      'gpuCount': 1,
      'status': 'Available',
      'managed': true,
    },
    {
      'name': 'coredns',
      'namespace': 'kube-system',
      'ready': 1,
      'replicas': 1,
      'image': 'coredns:1.11',
      'cpu': '100m',
      'memory': '70Mi',
      'gpuResource': '',
      'gpuCount': 0,
      'status': 'Available',
      'managed': false,
    },
  ],
  'pods': [
    {
      'name': 'web-abc',
      'namespace': 'default',
      'phase': 'Running',
      'reason': '',
      'node': 'node-1',
    },
    {
      'name': 'web-def',
      'namespace': 'default',
      'phase': 'Pending',
      'reason': 'ImagePullBackOff',
      'node': '',
    },
  ],
  'events': [
    {
      'namespace': 'default',
      'reason': 'Scheduled',
      'message': 'assigned to node-1',
      'object': 'web-abc',
      'type': 'Normal',
    },
  ],
  'truncated': false,
};

class _Fake extends http.BaseClient {
  final requests = <http.Request>[];
  final (int, String) Function(http.Request) respond;
  _Fake(this.respond);

  List<String> get calls => [
    for (final r in requests) '${r.method} ${r.url.path}?${r.url.query}',
  ];

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final req = request as http.Request;
    requests.add(req);
    final (code, body) = respond(req);
    return http.StreamedResponse(Stream.value(utf8.encode(body)), code);
  }
}

(int, String) _defaultRoutes(http.Request req) {
  final path = req.url.path;
  if (path == '/api/kubernetes/clusters') {
    return (200, jsonEncode([_cluster]));
  }
  if (path.endsWith('/overview')) return (200, jsonEncode(_overview()));
  if (path.endsWith('/logs')) return (200, 'hello from web-abc');
  if (path.endsWith('/workloads')) {
    final dry = req.url.queryParameters['dryRun'] == 'true';
    return (
      dry ? 200 : 201,
      jsonEncode({
        'name': 'api',
        'schedule': {
          'fits': true,
          'placements': {'node-1': 1},
        },
      }),
    );
  }
  if (path.endsWith('/helm')) {
    return (
      200,
      jsonEncode([
        {
          'name': 'redis',
          'namespace': 'default',
          'chart': 'redis',
          'chartVersion': '19.0.1',
          'status': 'deployed',
          'revision': 2,
        },
      ]),
    );
  }
  return (404, 'not found');
}

Future<_Fake> _pump(
  WidgetTester tester,
  Widget Function(ApiClient) screen, {
  (int, String) Function(http.Request)? routes,
}) async {
  final fake = _Fake(routes ?? _defaultRoutes);
  final client = ApiClient(baseUrl: 'http://localhost:8080', httpClient: fake);
  tester.view.physicalSize = const Size(1400, 2400);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(MaterialApp(home: screen(client)));
  await tester.pumpAndSettle();
  return fake;
}

void main() {
  group('cluster list', () {
    testWidgets('admin sees clusters with connect and disconnect actions', (
      tester,
    ) async {
      await _pump(
        tester,
        (api) => KubernetesScreen(apiClient: api, isAdmin: true),
      );

      expect(find.text('lab'), findsOneWidget);
      expect(find.text('https://k8s.lab:6443'), findsOneWidget);
      expect(find.byTooltip('Add cluster'), findsOneWidget);
      expect(find.byTooltip('Disconnect'), findsOneWidget);
    });

    testWidgets('non-admin can browse but not connect or disconnect', (
      tester,
    ) async {
      await _pump(
        tester,
        (api) => KubernetesScreen(apiClient: api, isAdmin: false),
      );

      expect(find.text('lab'), findsOneWidget);
      expect(find.byTooltip('Add cluster'), findsNothing);
      expect(find.byTooltip('Disconnect'), findsNothing);
    });

    testWidgets('empty state renders', (tester) async {
      await _pump(
        tester,
        (api) => KubernetesScreen(apiClient: api, isAdmin: true),
        routes: (_) => (200, '[]'),
      );
      expect(find.text('No Kubernetes clusters connected.'), findsOneWidget);
    });

    testWidgets('load failure renders the server error', (tester) async {
      await _pump(
        tester,
        (api) => KubernetesScreen(apiClient: api, isAdmin: true),
        routes: (_) => (500, 'database unavailable'),
      );
      expect(find.textContaining('database unavailable'), findsOneWidget);
    });

    testWidgets('connect sends name and kubeconfig, then refreshes', (
      tester,
    ) async {
      final fake = await _pump(
        tester,
        (api) => KubernetesScreen(apiClient: api, isAdmin: true),
        routes: (req) => req.method == 'POST'
            ? (201, jsonEncode(_cluster))
            : (200, jsonEncode([_cluster])),
      );

      await tester.tap(find.byTooltip('Add cluster'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Connect existing cluster'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.widgetWithText(TextField, 'Cluster name'),
        '  lab  ',
      );
      await tester.enterText(
        find.widgetWithText(TextField, 'Kubeconfig YAML'),
        'apiVersion: v1',
      );
      await tester.tap(find.text('Connect'));
      await tester.pumpAndSettle();

      final post = fake.requests.singleWhere((r) => r.method == 'POST');
      expect(jsonDecode(post.body), {
        'name': 'lab',
        'kubeconfig': 'apiVersion: v1',
      });
      expect(
        fake.calls.where((c) => c.startsWith('GET /api/kubernetes/clusters')),
        hasLength(2),
      );
      // A failed refresh would surface here as an error SnackBar.
      expect(find.byType(SnackBar), findsNothing);
    });

    testWidgets('local cluster create and delete use managed routes', (
      tester,
    ) async {
      const local = {
        'id': 'c2',
        'name': 'local',
        'apiServer': 'https://127.0.0.1:12345',
        'localKindName': 'pspe-test',
      };
      var created = false;
      final fake = await _pump(
        tester,
        (api) => KubernetesScreen(apiClient: api, isAdmin: true),
        routes: (req) {
          if (req.url.path == '/api/kubernetes/local-clusters' &&
              req.method == 'POST') {
            created = true;
            return (201, jsonEncode(local));
          }
          if (req.url.path == '/api/kubernetes/local-clusters/c2' &&
              req.method == 'DELETE') {
            created = false;
            return (204, '');
          }
          return (200, jsonEncode(created ? [local] : []));
        },
      );

      await tester.tap(find.byTooltip('Add cluster'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Create local cluster'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.widgetWithText(TextField, 'Cluster name'),
        'local',
      );
      await tester.tap(find.widgetWithText(FilledButton, 'Create'));
      await tester.pumpAndSettle();
      expect(fake.calls, contains('POST /api/kubernetes/local-clusters?'));
      expect(find.text('local'), findsOneWidget);

      await tester.tap(find.byTooltip('Delete local cluster'));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(FilledButton, 'Delete cluster'));
      await tester.pumpAndSettle();
      expect(fake.calls, contains('DELETE /api/kubernetes/local-clusters/c2?'));
    });

    testWidgets('disconnect asks for confirmation before deleting', (
      tester,
    ) async {
      final fake = await _pump(
        tester,
        (api) => KubernetesScreen(apiClient: api, isAdmin: true),
        routes: (req) =>
            req.method == 'DELETE' ? (204, '') : (200, jsonEncode([_cluster])),
      );

      await tester.tap(find.byTooltip('Disconnect'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Cancel'));
      await tester.pumpAndSettle();
      expect(fake.requests.where((r) => r.method == 'DELETE'), isEmpty);

      await tester.tap(find.byTooltip('Disconnect'));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(FilledButton, 'Disconnect'));
      await tester.pumpAndSettle();
      expect(fake.calls, contains('DELETE /api/kubernetes/clusters/c1?'));
    });

    testWidgets('tapping a cluster opens its overview', (tester) async {
      await _pump(
        tester,
        (api) => KubernetesScreen(apiClient: api, isAdmin: true),
      );

      await tester.tap(find.text('lab'));
      await tester.pumpAndSettle();

      expect(find.text('Deployments (2)'), findsOneWidget);
    });
  });

  group('cluster overview', () {
    Widget overview(ApiClient api, {bool isAdmin = true}) =>
        KubernetesClusterScreen(
          apiClient: api,
          cluster: _cluster,
          isAdmin: isAdmin,
        );

    testWidgets('populates every section with counts', (tester) async {
      await _pump(tester, overview);

      expect(find.text('Nodes (1)'), findsOneWidget);
      expect(find.text('Deployments (2)'), findsOneWidget);
      expect(find.text('Pods (2)'), findsOneWidget);
      expect(find.text('Events (1)'), findsOneWidget);
      // Deployments and Pods start expanded.
      expect(find.text('default/web'), findsOneWidget);
      expect(find.text('nginx:1.27 · 2/2 ready · Available'), findsOneWidget);
      expect(find.text('Running · node-1'), findsOneWidget);
      expect(find.text('Pending · ImagePullBackOff · '), findsOneWidget);

      await tester.tap(find.text('Nodes (1)'));
      await tester.tap(find.text('Events (1)'));
      await tester.pumpAndSettle();
      expect(find.text('node-1'), findsOneWidget);
      expect(find.textContaining('GPU {nvidia.com/gpu: 1}'), findsOneWidget);
      expect(find.text('Scheduled · web-abc'), findsOneWidget);
    });

    testWidgets('only managed deployments get admin actions', (tester) async {
      await _pump(tester, overview);

      expect(find.byTooltip('Deployment actions'), findsOneWidget);
      expect(find.text('Deploy workload'), findsOneWidget);
      expect(find.text('Deploy Ollama'), findsOneWidget);
      expect(find.text('Helm releases'), findsOneWidget);
    });

    testWidgets('viewers see no mutating controls', (tester) async {
      await _pump(tester, (api) => overview(api, isAdmin: false));

      expect(find.byTooltip('Deployment actions'), findsNothing);
      expect(find.text('Deploy workload'), findsNothing);
      expect(find.text('Deploy Ollama'), findsNothing);
      expect(find.text('Helm releases'), findsOneWidget);
    });

    testWidgets('namespace filter reloads the overview', (tester) async {
      final fake = await _pump(tester, overview);

      await tester.tap(find.text('All namespaces').first);
      await tester.pumpAndSettle();
      await tester.tap(find.text('team-a').last);
      await tester.pumpAndSettle();

      expect(
        fake.calls.last,
        'GET /api/kubernetes/clusters/c1/overview?namespace=team-a',
      );
    });

    testWidgets('pod logs open in a dialog', (tester) async {
      final fake = await _pump(tester, overview);

      await tester.tap(find.byTooltip('View logs').first);
      await tester.pumpAndSettle();

      expect(find.text('web-abc logs'), findsOneWidget);
      expect(find.text('hello from web-abc'), findsOneWidget);
      expect(
        fake.calls.last,
        'GET /api/kubernetes/clusters/c1/pods/default/web-abc/logs?',
      );
    });

    testWidgets('overview failure is shown instead of a spinner', (
      tester,
    ) async {
      await _pump(
        tester,
        overview,
        routes: (_) => (403, 'Kubernetes credentials lack permission'),
      );

      expect(
        find.textContaining('Kubernetes credentials lack permission'),
        findsOneWidget,
      );
    });

    testWidgets('deploy previews with a dry run before applying', (
      tester,
    ) async {
      final fake = await _pump(tester, overview);

      await tester.tap(find.text('Deploy workload'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.widgetWithText(TextField, 'Deployment name'),
        'api',
      );
      await tester.enterText(
        find.widgetWithText(TextField, 'Container image'),
        'example/api:1',
      );
      await tester.tap(find.widgetWithText(FilledButton, 'Deploy'));
      await tester.pumpAndSettle();

      expect(find.text('Review deployment'), findsOneWidget);
      expect(
        find.textContaining('Estimated placement: {node-1: 1}'),
        findsOneWidget,
      );
      final dryRuns = fake.requests.where((r) => r.method == 'POST').toList();
      expect(dryRuns, hasLength(1));
      expect(dryRuns.single.url.queryParameters['dryRun'], 'true');
      expect(jsonDecode(dryRuns.single.body), {
        'namespace': 'default',
        'name': 'api',
        'image': 'example/api:1',
        'cpu': '250m',
        'memory': '512Mi',
        'replicas': 1,
        'gpuCount': 0,
        'gpuResource': '',
      });

      await tester.tap(find.text('Apply'));
      await tester.pumpAndSettle();

      final posts = fake.requests.where((r) => r.method == 'POST').toList();
      expect(posts, hasLength(2));
      expect(posts.last.url.hasQuery, isFalse);
      expect(find.text('Deploy to Kubernetes'), findsNothing);
    });

    testWidgets('backing out of the review does not deploy', (tester) async {
      final fake = await _pump(tester, overview);

      await tester.tap(find.text('Deploy workload'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.widgetWithText(TextField, 'Deployment name'),
        'api',
      );
      await tester.enterText(
        find.widgetWithText(TextField, 'Container image'),
        'example/api:1',
      );
      await tester.tap(find.widgetWithText(FilledButton, 'Deploy'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Back'));
      await tester.pumpAndSettle();

      expect(fake.requests.where((r) => r.method == 'POST'), hasLength(1));
      // The form stays open and usable for another attempt.
      expect(find.text('Deploy to Kubernetes'), findsOneWidget);
      expect(find.widgetWithText(FilledButton, 'Deploy'), findsOneWidget);
    });

    testWidgets('a rejected preview keeps the form open with the reason', (
      tester,
    ) async {
      await _pump(
        tester,
        overview,
        routes: (req) => req.method == 'POST'
            ? (
                422,
                jsonEncode({
                  'error': 'workload does not currently fit the cluster',
                }),
              )
            : _defaultRoutes(req),
      );

      await tester.tap(find.text('Deploy workload'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.widgetWithText(TextField, 'Deployment name'),
        'api',
      );
      await tester.tap(find.widgetWithText(FilledButton, 'Deploy'));
      await tester.pumpAndSettle();

      expect(
        find.textContaining('workload does not currently fit the cluster'),
        findsOneWidget,
      );
      expect(find.text('Deploy to Kubernetes'), findsOneWidget);
    });

    testWidgets('update prefills the managed deployment and uses PUT', (
      tester,
    ) async {
      final fake = await _pump(
        tester,
        overview,
        routes: (req) => req.method == 'PUT'
            ? (
                200,
                jsonEncode({
                  'schedule': {'placements': {}},
                }),
              )
            : _defaultRoutes(req),
      );

      await tester.tap(find.byTooltip('Deployment actions'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Update'));
      await tester.pumpAndSettle();

      expect(find.text('Update deployment'), findsOneWidget);
      expect(find.widgetWithText(TextField, 'nginx:1.27'), findsOneWidget);
      await tester.enterText(
        find.widgetWithText(TextField, 'Container image'),
        'nginx:1.28',
      );
      await tester.tap(find.widgetWithText(FilledButton, 'Update'));
      await tester.pumpAndSettle();
      expect(find.textContaining('Current image: nginx:1.27'), findsOneWidget);
      await tester.tap(find.text('Apply'));
      await tester.pumpAndSettle();

      final puts = fake.requests.where((r) => r.method == 'PUT').toList();
      expect(puts, hasLength(2));
      expect(
        puts.first.url.path,
        '/api/kubernetes/clusters/c1/workloads/default/web',
      );
      expect(jsonDecode(puts.last.body), containsPair('image', 'nginx:1.28'));
      expect(
        jsonDecode(puts.last.body),
        containsPair('gpuResource', 'nvidia.com/gpu'),
      );
      expect(jsonDecode(puts.last.body), containsPair('gpuCount', 1));
    });

    testWidgets('rollback previews then applies the chosen revision', (
      tester,
    ) async {
      final fake = await _pump(
        tester,
        overview,
        routes: (req) {
          if (req.url.path.endsWith('/revisions')) {
            return (
              200,
              jsonEncode([
                {
                  'revision': 2,
                  'image': 'nginx:1.27',
                  'createdAt': 't2',
                  'current': true,
                },
                {
                  'revision': 1,
                  'image': 'nginx:1.26',
                  'createdAt': 't1',
                  'current': false,
                },
              ]),
            );
          }
          if (req.url.path.endsWith('/rollback')) return (200, '{}');
          return _defaultRoutes(req);
        },
      );

      await tester.tap(find.byTooltip('Deployment actions'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('History and rollback'));
      await tester.pumpAndSettle();
      expect(find.text('Current'), findsOneWidget);
      await tester.tap(find.text('Revision 1 · nginx:1.26'));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(FilledButton, 'Roll back'));
      await tester.pumpAndSettle();

      final rollbacks = fake.requests
          .where((r) => r.url.path.endsWith('/rollback'))
          .toList();
      expect(rollbacks, hasLength(2));
      expect(rollbacks.first.url.queryParameters['dryRun'], 'true');
      expect(rollbacks.last.url.hasQuery, isFalse);
      expect(jsonDecode(rollbacks.last.body), {'revision': 1});
    });

    testWidgets('delete is confirmed and targets the deployment', (
      tester,
    ) async {
      final fake = await _pump(
        tester,
        overview,
        routes: (req) =>
            req.method == 'DELETE' ? (204, '') : _defaultRoutes(req),
      );

      await tester.tap(find.byTooltip('Deployment actions'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Delete'));
      await tester.pumpAndSettle();
      expect(find.text('Delete deployment?'), findsOneWidget);
      await tester.tap(find.widgetWithText(FilledButton, 'Delete'));
      await tester.pumpAndSettle();

      expect(
        fake.calls,
        contains('DELETE /api/kubernetes/clusters/c1/workloads/default/web?'),
      );
    });

    testWidgets('Ollama deploy offers GPU choice and previews first', (
      tester,
    ) async {
      final fake = await _pump(
        tester,
        overview,
        routes: (req) {
          if (req.url.path.endsWith('/ai/ollama')) {
            final dry = req.url.queryParameters['dryRun'] == 'true';
            return (
              dry ? 200 : 201,
              jsonEncode({
                'schedule': {
                  'placements': {'node-1': 1},
                },
              }),
            );
          }
          return _defaultRoutes(req);
        },
      );

      await tester.tap(find.text('Deploy Ollama'));
      await tester.pumpAndSettle();
      expect(find.text('GPU resource (optional)'), findsOneWidget);
      await tester.enterText(
        find.widgetWithText(TextField, 'Ollama model (e.g. llama3.2:1b)'),
        'llama3.2:1b',
      );
      await tester.tap(find.text('Review'));
      await tester.pumpAndSettle();
      expect(find.text('Review model service'), findsOneWidget);
      await tester.tap(find.widgetWithText(FilledButton, 'Deploy'));
      await tester.pumpAndSettle();

      final posts = fake.requests
          .where((r) => r.url.path.endsWith('/ai/ollama'))
          .toList();
      expect(posts, hasLength(2));
      expect(posts.first.url.queryParameters['dryRun'], 'true');
      expect(jsonDecode(posts.last.body), {
        'namespace': 'default',
        'name': 'ollama',
        'model': 'llama3.2:1b',
        'cpu': '2',
        'memory': '4Gi',
        'storageGi': 20,
        'gpuResource': '',
        'gpuCount': 0,
      });
    });

    testWidgets('Helm releases button opens the namespace-scoped list', (
      tester,
    ) async {
      final fake = await _pump(tester, overview);

      await tester.tap(find.text('Helm releases'));
      await tester.pumpAndSettle();

      expect(find.text('lab · Helm'), findsOneWidget);
      expect(find.text('redis'), findsOneWidget);
      expect(
        fake.calls.last,
        'GET /api/kubernetes/clusters/c1/helm?namespace=default',
      );
    });
  });

  group('helm releases', () {
    Widget helm(ApiClient api, {bool isAdmin = true}) => HelmReleasesScreen(
      apiClient: api,
      clusterId: 'c1',
      clusterName: 'lab',
      namespaces: const ['default', 'team-a'],
      initialNamespace: 'default',
      isAdmin: isAdmin,
    );

    testWidgets('lists releases with chart, status and revision', (
      tester,
    ) async {
      await _pump(tester, helm);

      expect(find.text('redis'), findsOneWidget);
      expect(find.text('redis 19.0.1 · deployed · revision 2'), findsOneWidget);
      expect(find.byTooltip('Install or upgrade'), findsOneWidget);
    });

    testWidgets('viewers get no install or release actions', (tester) async {
      await _pump(tester, (api) => helm(api, isAdmin: false));

      expect(find.byTooltip('Install or upgrade'), findsNothing);
      expect(find.byType(PopupMenuButton<String>), findsNothing);
    });

    testWidgets('switching namespace reloads, with an empty state', (
      tester,
    ) async {
      final fake = await _pump(
        tester,
        helm,
        routes: (req) => req.url.queryParameters['namespace'] == 'team-a'
            ? (200, '[]')
            : _defaultRoutes(req),
      );

      await tester.tap(find.text('default'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('team-a').last);
      await tester.pumpAndSettle();

      expect(find.text('No Helm releases in this namespace.'), findsOneWidget);
      expect(
        fake.calls.last,
        'GET /api/kubernetes/clusters/c1/helm?namespace=team-a',
      );
    });

    testWidgets('install requires a chart archive before calling the API', (
      tester,
    ) async {
      final fake = await _pump(tester, helm);

      await tester.tap(find.byTooltip('Install or upgrade'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Review'));
      await tester.pumpAndSettle();

      expect(find.text('Select a .tgz Helm chart first.'), findsOneWidget);
      expect(fake.requests.where((r) => r.method == 'POST'), isEmpty);
    });

    testWidgets('history rolls back to the chosen revision after confirm', (
      tester,
    ) async {
      final fake = await _pump(
        tester,
        helm,
        routes: (req) {
          if (req.url.path.endsWith('/history')) {
            return (
              200,
              jsonEncode([
                {'revision': 2, 'chartVersion': '19.0.1', 'status': 'deployed'},
                {
                  'revision': 1,
                  'chartVersion': '18.0.0',
                  'status': 'superseded',
                },
              ]),
            );
          }
          if (req.url.path.endsWith('/rollback')) return (204, '');
          return _defaultRoutes(req);
        },
      );

      await tester.tap(find.byType(PopupMenuButton<String>));
      await tester.pumpAndSettle();
      await tester.tap(find.text('History and rollback'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Revision 1 · 18.0.0'));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(FilledButton, 'Roll back'));
      await tester.pumpAndSettle();

      final rollback = fake.requests.singleWhere(
        (r) => r.url.path.endsWith('/rollback'),
      );
      expect(
        rollback.url.path,
        '/api/kubernetes/clusters/c1/helm/default/redis/rollback',
      );
      expect(jsonDecode(rollback.body), {'revision': 1});
    });

    testWidgets('uninstall is confirmed first', (tester) async {
      final fake = await _pump(
        tester,
        helm,
        routes: (req) =>
            req.method == 'DELETE' ? (204, '') : _defaultRoutes(req),
      );

      await tester.tap(find.byType(PopupMenuButton<String>));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Uninstall'));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(FilledButton, 'Uninstall'));
      await tester.pumpAndSettle();

      expect(
        fake.calls,
        contains('DELETE /api/kubernetes/clusters/c1/helm/default/redis?'),
      );
    });
  });

  group('stale selections', () {
    Widget overview(ApiClient api) => KubernetesClusterScreen(
      apiClient: api,
      cluster: _cluster,
      isAdmin: true,
    );

    testWidgets(
      'updating a deployment whose GPU type is no longer advertised',
      (tester) async {
        final noGpuNodes = _overview()
          ..['nodes'] = [
            {
              'name': 'node-1',
              'ready': true,
              'cpu': '4',
              'memory': '8Gi',
              'gpu': {},
            },
          ];
        final fake = await _pump(
          tester,
          overview,
          routes: (req) => req.url.path.endsWith('/overview')
              ? (200, jsonEncode(noGpuNodes))
              : req.method == 'PUT'
              ? (
                  200,
                  jsonEncode({
                    'schedule': {'placements': {}},
                  }),
                )
              : _defaultRoutes(req),
        );

        await tester.tap(find.byTooltip('Deployment actions'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('Update'));
        await tester.pumpAndSettle();

        expect(tester.takeException(), isNull);
        expect(find.text('nvidia.com/gpu'), findsOneWidget);
        await tester.tap(find.widgetWithText(FilledButton, 'Update'));
        await tester.pumpAndSettle();
        final put = fake.requests.firstWhere((r) => r.method == 'PUT');
        expect(
          jsonDecode(put.body),
          containsPair('gpuResource', 'nvidia.com/gpu'),
        );
      },
    );

    testWidgets('filtered namespace deleted between refreshes', (tester) async {
      var deleted = false;
      await _pump(
        tester,
        overview,
        routes: (req) {
          if (req.url.path.endsWith('/overview') && deleted) {
            return (200, jsonEncode(_overview()..['namespaces'] = ['default']));
          }
          return _defaultRoutes(req);
        },
      );

      await tester.tap(find.text('All namespaces').first);
      await tester.pumpAndSettle();
      await tester.tap(find.text('team-a').last);
      await tester.pumpAndSettle();
      deleted = true;
      await tester.tap(find.byTooltip('Refresh'));
      await tester.pumpAndSettle();

      expect(tester.takeException(), isNull);
      expect(find.text('team-a'), findsOneWidget);
    });
  });
}
