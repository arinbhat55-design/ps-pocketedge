import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:app/api/api_client.dart';
import 'package:app/features/containers/container_insights_panel.dart';
import 'package:app/models/resource_insights.dart';

void main() {
  testWidgets(
    'shows anomalies and recommendations, and offers suggested limits',
    (tester) async {
      final fake = _FakeClient();
      ResourceLimits? reviewed;
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: SingleChildScrollView(
              child: ContainerInsightsPanel(
                apiClient: ApiClient(
                  baseUrl: 'http://localhost:8080',
                  httpClient: fake,
                ),
                serverId: 's1',
                containerId: 'c1',
                onReviewLimits: (suggested) async {
                  reviewed = suggested;
                  return true;
                },
              ),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();

      expect(fake.lastUri?.path, '/api/servers/s1/containers/c1/insights');
      expect(find.text('CPU spike'), findsOneWidget);
      expect(find.textContaining('Memory limit: unlimited →'), findsOneWidget);
      expect(find.textContaining('High CPU'), findsOneWidget);

      await tester.tap(find.text('Review & apply'));
      await tester.pumpAndSettle();
      expect(reviewed?.memoryLimitBytes, 384 * 1024 * 1024);
    },
  );

  test('alert rule condition label', () {
    const rule = ContainerAlertRule(
      id: 'r1',
      name: 'Hot',
      metric: 'cpu',
      threshold: 90,
      durationSeconds: 300,
      severity: 'warning',
      enabled: true,
    );
    // Stored as percent of one core; shown in cores.
    expect(rule.conditionLabel, 'CPU > 0.90 cores for 5 min');
    expect(formatMetricValue('cpu', 150), '1.50 cores');
    expect(formatMetricValue('memory', 42), '42.0%');
  });
}

class _FakeClient extends http.BaseClient {
  Uri? lastUri;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    lastUri = request.url;
    final body = jsonEncode({
      'stats': {
        'sampleCount': 180,
        'from': '2026-09-28T11:00:00Z',
        'to': '2026-09-28T12:00:00Z',
        'cpuP50': 40,
        'cpuP95': 49,
        'cpuMax': 80,
        'memBytesP50': 262144000,
        'memBytesP95': 293601280,
        'memBytesMax': 304087040,
        'pidsMax': 20,
      },
      'anomalies': [
        {
          'metric': 'cpu',
          'kind': 'spike',
          'severity': 'critical',
          'current': 80,
          'baseline': 11,
          'message': 'CPU usage jumped to 80.0%.',
        },
      ],
      'recommendations': [
        {
          'resource': 'memory',
          'action': 'set',
          'severity': 'info',
          'current': 0,
          'suggested': 402653184,
          'message': 'No memory limit is set.',
        },
      ],
      'currentLimits': {},
      'suggestedLimits': {'memoryLimitBytes': 402653184},
      'openAlerts': [
        {
          'id': 'a1',
          'kind': 'threshold',
          'ruleName': 'High CPU',
          'serverId': 's1',
          'serverName': 'edge-1',
          'containerId': 'c1',
          'containerName': 'web',
          'metric': 'cpu',
          'severity': 'warning',
          'threshold': 70,
          'value': 80,
          'message': 'CPU is 80.0%.',
          'startedAt': '2026-09-28T11:55:00Z',
          'lastSeenAt': '2026-09-28T12:00:00Z',
        },
      ],
    });
    return http.StreamedResponse(Stream.value(utf8.encode(body)), 200);
  }
}
