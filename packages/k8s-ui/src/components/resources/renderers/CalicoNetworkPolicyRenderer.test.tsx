import { describe, expect, it } from 'vitest'
import { renderToString } from 'react-dom/server'
import { CalicoNetworkPolicyRenderer } from './CalicoNetworkPolicyRenderer'

describe('CalicoNetworkPolicyRenderer', () => {
  it('renders policy metadata and operational rule fields', () => {
    const html = renderToString(
      <CalicoNetworkPolicyRenderer
        data={{
          apiVersion: 'crd.projectcalico.org/v1',
          kind: 'StagedGlobalNetworkPolicy',
          spec: {
            selector: "app == 'api'",
            namespaceSelector: "project == 'prod'",
            serviceAccountSelector: "role == 'api'",
            tier: 'security',
            order: 100,
            types: ['Ingress', 'Egress'],
            stagedAction: 'Set',
            preDNAT: true,
            applyOnForward: true,
            doNotTrack: false,
            performanceHints: ['AssumeNeededOnEveryPacket'],
            ingress: [
              {
                action: 'Deny',
                protocol: 'TCP',
                notProtocol: 'UDP',
                source: {
                  selector: "role == 'frontend'",
                  notSelector: "app == 'debug'",
                  namespaceSelector: "team == 'payments'",
                  notNamespaceSelector: "env == 'test'",
                  nets: ['10.0.0.0/8'],
                  notNets: ['10.1.0.0/16'],
                  serviceAccounts: {
                    names: ['frontend'],
                    selector: "track == 'stable'",
                  },
                  notServiceAccounts: { names: ['debug'] },
                  ports: [80, { port: 443, protocol: 'TCP', endPort: 445 }],
                  notPorts: [8080],
                },
                http: {
                  methods: ['GET'],
                  notMethods: ['DELETE'],
                  paths: ['/api'],
                  notPaths: ['/admin'],
                },
                icmp: { type: 8, code: 0 },
                notICMP: { type: 3 },
              },
            ],
            egress: [
              {
                action: 'Allow',
                destination: {
                  selector: "app == 'db'",
                  nets: ['192.168.0.0/16'],
                  ports: ['5432'],
                },
              },
            ],
          },
        }}
      />,
    )

    for (const value of [
      'StagedGlobalNetworkPolicy',
      'Policy Flow',
      'app == &#x27;api&#x27;',
      'project == &#x27;prod&#x27;',
      'role == &#x27;api&#x27;',
      'security',
      '100',
      'Ingress',
      'Egress',
      'Deny',
      'Pre-DNAT',
      'Apply On Forward',
      'Do Not Track',
      'AssumeNeededOnEveryPacket',
      'Source',
      'role == &#x27;frontend&#x27;',
      'app == &#x27;debug&#x27;',
      'team == &#x27;payments&#x27;',
      'env == &#x27;test&#x27;',
      '10.0.0.0/8',
      '10.1.0.0/16',
      'frontend',
      'Source Ports',
      'Ports',
      'TCP/80',
      'TCP/443-445',
      'not TCP/8080',
      'HTTP',
      'GET',
      'DELETE',
      '/api',
      '/admin',
      'ICMP',
      'type 8, code 0',
      'Not ICMP',
      'type 3',
      'Destination',
      'db',
      '192.168.0.0/16',
      'TCP/5432',
    ]) {
      expect(html).toContain(value)
    }
    expect(html).toContain('Allow')
    expect(html).not.toContain('Rule 1')
    expect(html).not.toContain('Not Ports')
    expect(html).not.toMatch(/badge-sm[^>]*>TCP<\/span>/)
  })

  it('keeps a standalone protocol badge when detailed rules have no ports', () => {
    const html = renderToString(
      <CalicoNetworkPolicyRenderer
        data={{
          apiVersion: 'projectcalico.org/v3',
          kind: 'NetworkPolicy',
          spec: {
            selector: "app == 'api'",
            types: ['Ingress'],
            ingress: [
              {
                action: 'Log',
                protocol: 'TCP',
                notProtocol: 'UDP',
                source: { selector: "app == 'client'" },
              },
            ],
          },
        }}
      />,
    )

    expect(html).toMatch(/badge-sm[^>]*>TCP<\/span>/)
    expect(html).toContain('not')
    expect(html).toContain('UDP')
    expect(html).not.toContain('Source Ports')
  })

  it('does not show staged-only metadata for an ordinary policy', () => {
    const html = renderToString(
      <CalicoNetworkPolicyRenderer
        data={{ kind: 'NetworkPolicy', spec: { selector: 'all()' } }}
      />,
    )

    expect(html).toContain('CalicoNetworkPolicy')
    expect(html).not.toContain('Staged Action')
  })

  it('renders Tier as a navigable cluster-scoped Calico reference', () => {
    const html = renderToString(
      <CalicoNetworkPolicyRenderer
        data={{
          apiVersion: 'projectcalico.org/v3',
          kind: 'NetworkPolicy',
          spec: { tier: 'security' },
        }}
        onNavigate={() => {}}
      />,
    )

    expect(html).toMatch(/tier[\s\S]*security/)
    expect(html).toContain('security')
    expect(html).toContain('<button')
  })
})

describe('CalicoNetworkPolicyRenderer staged deletions', () => {
  it('says a deletion previews nothing instead of drawing a policy flow', () => {
    const html = renderToString(
      <CalicoNetworkPolicyRenderer
        data={{
          apiVersion: 'projectcalico.org/v3',
          kind: 'StagedNetworkPolicy',
          metadata: { name: 'db-lockdown', namespace: 'prod' },
          // The Calico API rejects every other spec field alongside Delete.
          spec: { stagedAction: 'Delete', tier: 'default' },
        }}
      />,
    )

    expect(html).toContain('Staged deletion')
    expect(html).toContain('Nothing is previewed as protected')
    expect(html).toContain('db-lockdown')
    expect(html).not.toContain('all workloads')
    // The flow diagram's own staged banner must not also render.
    expect(html).not.toContain('Dashed paths are evaluated but not enforced')
  })
})

describe('CalicoNetworkPolicyRenderer ignored staged policies', () => {
  it('does not present an ignored staged policy as a preview of protection', () => {
    const html = renderToString(
      <CalicoNetworkPolicyRenderer
        data={{
          apiVersion: 'projectcalico.org/v3',
          kind: 'StagedNetworkPolicy',
          metadata: { name: 'parked', namespace: 'prod' },
          // Ignore keeps its rules, but Calico skips the policy, so the rules
          // preview nothing.
          spec: {
            stagedAction: 'Ignore',
            selector: "app == 'web'",
            types: ['Ingress'],
            ingress: [{ action: 'Allow' }],
          },
        }}
      />,
    )

    expect(html).toContain('Staged, ignored')
    expect(html).toContain('Nothing is previewed as protected')
    expect(html).not.toContain('Dashed paths are evaluated but not enforced')
  })
})
