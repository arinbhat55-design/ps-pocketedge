import 'dart:convert';

import 'package:app/api/api_client.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

void main() {
  group('governed deployment actions', () {
    test(
      'a maintenance-window refusal surfaces as MaintenanceWindowException',
      () async {
        final client = ApiClient(
          baseUrl: 'http://localhost:8080',
          authToken: 'token',
          httpClient: _JsonClient(409, {
            'error':
                'the production environment only allows changes during its maintenance window',
            'outsideMaintenanceWindow': true,
            'canOverride': true,
            'nextWindow': '2026-09-27T02:00:00Z',
          }),
        );
        await expectLater(
          client.redeployDeployment('d1'),
          throwsA(
            isA<MaintenanceWindowException>()
                .having((e) => e.canOverride, 'canOverride', isTrue)
                .having(
                  (e) => e.nextWindow,
                  'nextWindow',
                  DateTime.utc(2026, 9, 27, 2),
                ),
          ),
        );
      },
    );

    test('a queued outcome is returned, not thrown', () async {
      final client = ApiClient(
        baseUrl: 'http://localhost:8080',
        authToken: 'token',
        httpClient: _JsonClient(202, {
          'deploymentId': 'd1',
          'status': 'pending_approval',
          'requestId': 'r1',
        }),
      );
      final outcome = await client.redeployDeployment('d1');
      expect(outcome.isQueued, isTrue);
      expect(outcome.requestId, 'r1');
    });

    test('other errors use the JSON error message', () async {
      final client = ApiClient(
        baseUrl: 'http://localhost:8080',
        authToken: 'token',
        httpClient: _JsonClient(400, {
          'error':
              'the production environment requires a change request reference',
        }),
      );
      await expectLater(
        client.scaleService('d1', 'web', 2),
        throwsA(
          isA<ApiException>().having(
            (e) => e.message,
            'message',
            contains('change request'),
          ),
        ),
      );
    });
  });

  group('aiConfigured', () {
    test(
      'returns true when the control plane reports AI is configured',
      () async {
        final client = ApiClient(
          baseUrl: 'http://localhost:8080',
          authToken: 'token',
          httpClient: _JsonClient(200, {'configured': true}),
        );
        expect(await client.aiConfigured(), isTrue);
      },
    );

    test(
      'returns false when the control plane reports AI is not configured',
      () async {
        final client = ApiClient(
          baseUrl: 'http://localhost:8080',
          authToken: 'token',
          httpClient: _JsonClient(200, {'configured': false}),
        );
        expect(await client.aiConfigured(), isFalse);
      },
    );

    test('returns false (not a thrown error) on a non-200 response, so the '
        'AI button just stays hidden rather than the app crashing', () async {
      final client = ApiClient(
        baseUrl: 'http://localhost:8080',
        authToken: 'token',
        httpClient: _JsonClient(500, {'error': 'boom'}),
      );
      expect(await client.aiConfigured(), isFalse);
    });
  });

  group('analyzeContainerLogs', () {
    test('parses a successful analysis response', () async {
      final client = ApiClient(
        baseUrl: 'http://localhost:8080',
        authToken: 'token',
        httpClient: _JsonClient(200, {
          'summary': 'ok',
          'rootCause': 'none',
          'recommendation': 'n/a',
        }),
      );

      final analysis = await client.analyzeContainerLogs('srv1', 'c1');
      expect(analysis.summary, 'ok');
    });

    test('throws ApiException carrying the error message on failure', () async {
      final client = ApiClient(
        baseUrl: 'http://localhost:8080',
        authToken: 'token',
        httpClient: _JsonClient(501, {
          'error': 'AI features are not configured',
        }),
      );

      expect(
        () => client.analyzeContainerLogs('srv1', 'c1'),
        throwsA(
          isA<ApiException>().having(
            (e) => e.message,
            'message',
            'AI features are not configured',
          ),
        ),
      );
    });
  });

  group('listContainerEvents', () {
    test('parses a list of events', () async {
      final client = ApiClient(
        baseUrl: 'http://localhost:8080',
        authToken: 'token',
        httpClient: _JsonListClient(200, [
          {
            'timestampUnix': 1700000000,
            'type': 'container',
            'action': 'start',
            'containerId': 'c1',
            'containerName': 'web',
          },
        ]),
      );

      final events = await client.listContainerEvents('srv1', 'c1');
      expect(events, hasLength(1));
      expect(events.single.action, 'start');
    });

    test('includes since/until as ISO-8601 UTC query params', () async {
      final capturing = _CapturingClient(200, jsonEncode(<dynamic>[]));
      final client = ApiClient(
        baseUrl: 'http://localhost:8080',
        authToken: 'token',
        httpClient: capturing,
      );

      final since = DateTime.utc(2024, 1, 1);
      final until = DateTime.utc(2024, 1, 2);
      await client.listContainerEvents(
        'srv1',
        'c1',
        since: since,
        until: until,
      );

      expect(capturing.lastRequest, isNotNull);
      final uri = capturing.lastRequest!.url;
      expect(uri.queryParameters['since'], since.toIso8601String());
      expect(uri.queryParameters['until'], until.toIso8601String());
    });
  });

  group('downloadContainerLogs', () {
    test('returns the raw plain-text body', () async {
      final client = ApiClient(
        baseUrl: 'http://localhost:8080',
        authToken: 'token',
        httpClient: _RawClient(200, '2024-01-02T15:04:05Z [stdout] hello\n'),
      );

      final text = await client.downloadContainerLogs('srv1', 'c1');
      expect(text, '2024-01-02T15:04:05Z [stdout] hello\n');
    });

    test('throws ApiException on a non-200 response', () async {
      final client = ApiClient(
        baseUrl: 'http://localhost:8080',
        authToken: 'token',
        httpClient: _RawClient(409, '{"error":"server not connected"}'),
      );

      expect(
        () => client.downloadContainerLogs('srv1', 'c1'),
        throwsA(isA<ApiException>()),
      );
    });
  });

  group('containerLogsStreamUri', () {
    test('builds a ws:// URL with token, follow, tail, and with= params', () {
      final client = ApiClient(
        baseUrl: 'http://localhost:8080',
        authToken: 'tok',
      );
      final uri = client.containerLogsStreamUri(
        'srv1',
        'c1',
        follow: false,
        tail: 100,
        withContainers: ['c2', 'c3'],
      );

      expect(uri.scheme, 'ws');
      expect(uri.path, '/api/servers/srv1/containers/c1/logs/stream');
      expect(uri.queryParameters['token'], 'tok');
      expect(uri.queryParameters['follow'], 'false');
      expect(uri.queryParameters['tail'], '100');
      expect(uri.queryParameters['with'], 'c2,c3');
    });

    test('uses wss:// when baseUrl is https', () {
      final client = ApiClient(
        baseUrl: 'https://example.com',
        authToken: 'tok',
      );
      final uri = client.containerLogsStreamUri('srv1', 'c1');
      expect(uri.scheme, 'wss');
    });

    test('omits follow/tail/with when not specified (defaults, live tail)', () {
      final client = ApiClient(
        baseUrl: 'http://localhost:8080',
        authToken: 'tok',
      );
      final uri = client.containerLogsStreamUri('srv1', 'c1');
      expect(uri.queryParameters.containsKey('follow'), isFalse);
      expect(uri.queryParameters.containsKey('tail'), isFalse);
      expect(uri.queryParameters.containsKey('with'), isFalse);
    });
  });

  group('containerExecUri', () {
    test('builds a ws:// URL with token, cols, and rows', () {
      final client = ApiClient(
        baseUrl: 'http://localhost:8080',
        authToken: 'tok',
      );
      final uri = client.containerExecUri('srv1', 'c1', cols: 100, rows: 30);

      expect(uri.scheme, 'ws');
      expect(uri.path, '/api/servers/srv1/containers/c1/exec');
      expect(uri.queryParameters['token'], 'tok');
      expect(uri.queryParameters['cols'], '100');
      expect(uri.queryParameters['rows'], '30');
    });

    test('defaults to 80x24 when not specified', () {
      final client = ApiClient(
        baseUrl: 'http://localhost:8080',
        authToken: 'tok',
      );
      final uri = client.containerExecUri('srv1', 'c1');
      expect(uri.queryParameters['cols'], '80');
      expect(uri.queryParameters['rows'], '24');
    });
  });
}

/// Returns [status] with a JSON object body for every request.
class _JsonClient extends http.BaseClient {
  final int status;
  final Map<String, dynamic> body;
  _JsonClient(this.status, this.body);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    return http.StreamedResponse(
      Stream.value(utf8.encode(jsonEncode(body))),
      status,
    );
  }
}

/// Returns [status] with a JSON array body for every request.
class _JsonListClient extends http.BaseClient {
  final int status;
  final List<dynamic> body;
  _JsonListClient(this.status, this.body);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    return http.StreamedResponse(
      Stream.value(utf8.encode(jsonEncode(body))),
      status,
    );
  }
}

/// Returns [status] with a raw string body (not JSON-encoded) for every
/// request — used for the plain-text log-download endpoint.
class _RawClient extends http.BaseClient {
  final int status;
  final String body;
  _RawClient(this.status, this.body);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    return http.StreamedResponse(Stream.value(utf8.encode(body)), status);
  }
}

/// Records the last request it received, so a test can assert on the
/// method/URL/headers/body actually sent by ApiClient.
class _CapturingClient extends http.BaseClient {
  final int status;
  final String body;
  http.BaseRequest? lastRequest;
  _CapturingClient(this.status, this.body);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    lastRequest = request;
    return http.StreamedResponse(Stream.value(utf8.encode(body)), status);
  }
}
