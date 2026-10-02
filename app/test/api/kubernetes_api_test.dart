import 'dart:convert';

import 'package:app/api/api_client.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

/// Records every request and answers with the next scripted response.
class _Recorder extends http.BaseClient {
  final requests = <http.Request>[];
  final (int, String) Function(http.Request) respond;
  _Recorder(this.respond);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final req = request as http.Request;
    requests.add(req);
    final (code, body) = respond(req);
    return http.StreamedResponse(Stream.value(utf8.encode(body)), code);
  }
}

void main() {
  ApiClient client(_Recorder http) => ApiClient(
    baseUrl: 'http://localhost:8080',
    authToken: 'token',
    httpClient: http,
  );

  test('cluster CRUD uses the Kubernetes cluster routes', () async {
    final rec = _Recorder((req) {
      if (req.method == 'GET') {
        return (
          200,
          jsonEncode([
            {'id': 'c1', 'name': 'lab', 'apiServer': 'https://k8s:6443'},
          ]),
        );
      }
      if (req.method == 'POST') return (201, '{"id":"c1"}');
      return (204, '');
    });
    final api = client(rec);

    final clusters = await api.listKubernetesClusters();
    await api.addKubernetesCluster('lab', 'kubeconfig-yaml');
    await api.removeKubernetesCluster('c1');

    expect(clusters.single['apiServer'], 'https://k8s:6443');
    expect(rec.requests.map((r) => '${r.method} ${r.url.path}'), [
      'GET /api/kubernetes/clusters',
      'POST /api/kubernetes/clusters',
      'DELETE /api/kubernetes/clusters/c1',
    ]);
    expect(jsonDecode(rec.requests[1].body), {
      'name': 'lab',
      'kubeconfig': 'kubeconfig-yaml',
    });
    expect(rec.requests.first.headers['Authorization'], 'Bearer token');
  });

  test('overview passes the namespace filter only when set', () async {
    final rec = _Recorder((_) => (200, '{"namespaces":[]}'));
    final api = client(rec);

    await api.kubernetesOverview('c1');
    await api.kubernetesOverview('c1', namespace: 'team-a');

    expect(rec.requests[0].url.query, isEmpty);
    expect(rec.requests[1].url.queryParameters, {'namespace': 'team-a'});
    expect(rec.requests[1].url.path, '/api/kubernetes/clusters/c1/overview');
  });

  test(
    'workload deploy previews with dryRun and expects 200, then 201',
    () async {
      final rec = _Recorder(
        (req) => (
          req.url.queryParameters['dryRun'] == 'true' ? 200 : 201,
          '{"name":"web"}',
        ),
      );
      final api = client(rec);
      final spec = {'namespace': 'default', 'name': 'web'};

      await api.deployKubernetesWorkload('c1', spec, dryRun: true);
      await api.deployKubernetesWorkload('c1', spec);

      expect(
        rec.requests[0].url.toString(),
        endsWith('/workloads?dryRun=true'),
      );
      expect(rec.requests[1].url.hasQuery, isFalse);
      expect(jsonDecode(rec.requests[1].body), spec);
    },
  );

  test('workload update, revisions, rollback and delete target the workload '
      'path', () async {
    final rec = _Recorder((req) {
      if (req.method == 'DELETE') return (204, '');
      if (req.url.path.endsWith('/revisions')) {
        return (200, '[{"revision":2,"current":true}]');
      }
      return (200, '{}');
    });
    final api = client(rec);

    await api.updateKubernetesWorkload('c1', 'ns', 'web', {}, dryRun: true);
    final revisions = await api.kubernetesRevisions('c1', 'ns', 'web');
    await api.rollbackKubernetesWorkload('c1', 'ns', 'web', 1);
    await api.deleteKubernetesWorkload('c1', 'ns', 'web');

    expect(revisions.single['revision'], 2);
    expect(rec.requests.map((r) => '${r.method} ${r.url}'), [
      'PUT http://localhost:8080/api/kubernetes/clusters/c1/workloads/ns/web?dryRun=true',
      'GET http://localhost:8080/api/kubernetes/clusters/c1/workloads/ns/web/revisions',
      'POST http://localhost:8080/api/kubernetes/clusters/c1/workloads/ns/web/rollback',
      'DELETE http://localhost:8080/api/kubernetes/clusters/c1/workloads/ns/web',
    ]);
    expect(jsonDecode(rec.requests[2].body), {'revision': 1});
  });

  test('path segments are URL-encoded', () async {
    final rec = _Recorder((_) => (200, 'log line'));
    final logs = await client(rec).kubernetesPodLogs('c/1', 'ns', 'pod a');

    expect(logs, 'log line');
    expect(
      rec.requests.single.url.toString(),
      'http://localhost:8080/api/kubernetes/clusters/c%2F1/pods/ns/pod%20a/logs',
    );
  });

  test('helm routes and status codes', () async {
    final rec = _Recorder((req) {
      if (req.method == 'GET') return (200, '[]');
      if (req.url.path.endsWith('/helm')) return (200, '{"revision":1}');
      return (204, '');
    });
    final api = client(rec);

    await api.kubernetesHelmReleases('c1', 'default');
    await api.applyKubernetesHelmChart('c1', {'name': 'r'}, dryRun: true);
    await api.kubernetesHelmHistory('c1', 'default', 'r');
    await api.rollbackKubernetesHelm('c1', 'default', 'r', 3);
    await api.uninstallKubernetesHelm('c1', 'default', 'r');

    expect(rec.requests.map((r) => '${r.method} ${r.url}'), [
      'GET http://localhost:8080/api/kubernetes/clusters/c1/helm?namespace=default',
      'POST http://localhost:8080/api/kubernetes/clusters/c1/helm?dryRun=true',
      'GET http://localhost:8080/api/kubernetes/clusters/c1/helm/default/r/history',
      'POST http://localhost:8080/api/kubernetes/clusters/c1/helm/default/r/rollback',
      'DELETE http://localhost:8080/api/kubernetes/clusters/c1/helm/default/r',
    ]);
    expect(jsonDecode(rec.requests[3].body), {'revision': 3});
  });

  test('a scheduling rejection surfaces the server error message', () async {
    final rec = _Recorder(
      (_) => (
        422,
        jsonEncode({
          'error': 'workload does not currently fit the cluster',
          'schedule': {'fits': false},
        }),
      ),
    );

    await expectLater(
      client(rec).deployKubernetesWorkload('c1', {}, dryRun: true),
      throwsA(
        predicate(
          (e) => '$e'.contains('workload does not currently fit the cluster'),
        ),
      ),
    );
  });

  test('plain-text errors (e.g. invalid kubeconfig) surface as-is', () async {
    final rec = _Recorder(
      (_) => (400, 'insecure TLS verification is not allowed\n'),
    );

    await expectLater(
      client(rec).addKubernetesCluster('lab', 'cfg'),
      throwsA(
        predicate(
          (e) => '$e'.contains('insecure TLS verification is not allowed'),
        ),
      ),
    );
  });
}
