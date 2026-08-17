import { describe, it, expect } from 'vitest'
import {
  getKyvernoRequestType,
  getKyvernoRequestTriggers,
  getKyvernoRequestGenerated,
  getKyvernoRequestState,
  getKyvernoReportSubject,
  getKyvernoReportSummary,
  getKyvernoReportResults,
  getKyvernoReportStatus,
  kyvernoReportTimestamp,
} from './resource-utils-kyverno-queue'

/**
 * The fixtures below are trimmed copies of objects captured with a watch on a
 * live Kyverno 1.18.2 while a generateExisting rule and a mutate-existing rule
 * ran: 251 UpdateRequests and 3322 EphemeralReports. Every assertion here
 * exists because the obvious reading of the CRD produces a blank screen.
 */

const GENERATE_REQUEST = {
  metadata: { name: 'ur-j8qjv', namespace: 'kyverno', labels: { 'generate.kyverno.io/policy-name': 'probe-generate-existing' } },
  spec: {
    policy: 'probe-generate-existing',
    requestType: 'generate',
    deleteDownstream: false,
    // Both empty on every generate request observed.
    resource: {},
    rule: '',
    ruleContext: [
      { rule: 'companion-for-existing', deleteDownstream: false, trigger: { apiVersion: 'v1', kind: 'ConfigMap', name: 'bulk-001', namespace: 'policy-demo', uid: 'a' } },
      { rule: 'companion-for-existing', deleteDownstream: false, trigger: { apiVersion: 'v1', kind: 'ConfigMap', name: 'bulk-002', namespace: 'policy-demo', uid: 'b' } },
    ],
  },
  status: {
    state: 'Completed',
    generatedResources: [{ apiVersion: 'networking.k8s.io/v1', kind: 'NetworkPolicy', name: 'bulk-250-probe', namespace: 'policy-demo' }],
  },
}

const MUTATE_REQUEST = {
  metadata: { name: 'ur-2mhmq', namespace: 'kyverno' },
  spec: {
    policy: 'probe-mutate-existing',
    requestType: 'mutate',
    rule: 'label-existing-configmaps',
    deleteDownstream: false,
    resource: { apiVersion: 'v1', kind: 'ConfigMap', name: 'bulk-001', namespace: 'policy-demo', uid: 'a' },
  },
  status: { state: 'Completed' },
}

describe('UpdateRequest', () => {
  it('reads triggers off ruleContext for a generate request', () => {
    // spec.resource is {} here — reading it alone renders an empty page.
    const t = getKyvernoRequestTriggers(GENERATE_REQUEST)
    expect(t).toHaveLength(2)
    expect(t[0].name).toBe('bulk-001')
    expect(t[0].kind).toBe('ConfigMap')
    expect(t[0].rule).toBe('companion-for-existing')
  })

  it('reads the single trigger off spec.resource for a mutate request', () => {
    const t = getKyvernoRequestTriggers(MUTATE_REQUEST)
    expect(t).toHaveLength(1)
    expect(t[0].name).toBe('bulk-001')
  })

  it('reports no triggers rather than a blank row when neither is present', () => {
    expect(getKyvernoRequestTriggers({ spec: { requestType: 'generate', resource: {}, ruleContext: [] } })).toEqual([])
  })

  it('distinguishes the two request types', () => {
    expect(getKyvernoRequestType(GENERATE_REQUEST)).toBe('generate')
    expect(getKyvernoRequestType(MUTATE_REQUEST)).toBe('mutate')
    expect(getKyvernoRequestType({ spec: {} })).toBe('unknown')
  })

  it('lists what was created, and only on the request that creates', () => {
    expect(getKyvernoRequestGenerated(GENERATE_REQUEST)).toHaveLength(1)
    expect(getKyvernoRequestGenerated(MUTATE_REQUEST)).toEqual([])
  })

  // Pending is the state worth surfacing: work queued and never done.
  it('separates queued from done from failed', () => {
    expect(getKyvernoRequestState({ status: { state: 'Pending' } }).level).toBe('degraded')
    expect(getKyvernoRequestState({ status: { state: 'Completed' } }).level).toBe('healthy')
    expect(getKyvernoRequestState({ status: { state: 'Failed' } }).level).toBe('unhealthy')
    expect(getKyvernoRequestState({}).level).toBe('unknown')
  })
})

const REPORT = {
  metadata: {
    name: 'eae46cfc-2733-466c-b20a-f1a354b6f73b-7b47h',
    namespace: 'kube-system',
    labels: {
      'audit.kyverno.io/resource.kind': 'Pod',
      'audit.kyverno.io/resource.group': '',
      'audit.kyverno.io/resource.version': 'v1',
      'audit.kyverno.io/resource.uid': 'eae46cfc-2733-466c-b20a-f1a354b6f73b',
      'audit.kyverno.io/source': 'background-scan',
    },
    ownerReferences: [{ apiVersion: 'v1', kind: 'Pod', name: 'coredns-7d764666f9-dl4p6', uid: 'eae46cfc' }],
  },
  spec: {
    // Present and blank on every report observed.
    owner: { apiVersion: '', kind: '', name: '', uid: '' },
    summary: { error: 0, fail: 1, pass: 2, skip: 0, warn: 0 },
    results: [
      { message: 'mutation is not applied', policy: 'add-default-labels', result: 'fail', source: 'KyvernoMutatingPolicy', timestamp: { seconds: 1786622539, nanos: 0 } },
    ],
  },
}

describe('EphemeralReport', () => {
  // spec.owner is the documented field and is useless; the owner reference is
  // where the subject actually is.
  it('takes the subject from the owner reference when spec.owner is blank', () => {
    const s = getKyvernoReportSubject(REPORT)
    expect(s?.kind).toBe('Pod')
    expect(s?.name).toBe('coredns-7d764666f9-dl4p6')
    expect(s?.namespace).toBe('kube-system')
  })

  it('falls back to the labels when the subject is already gone', () => {
    const { ownerReferences, ...meta } = REPORT.metadata as any
    const s = getKyvernoReportSubject({ ...REPORT, metadata: meta })
    expect(s?.kind).toBe('Pod')
    expect(s?.name).toBeUndefined()
    expect(s?.apiVersion).toBe('v1')
  })

  it('prefers spec.owner when a future version starts filling it in', () => {
    const s = getKyvernoReportSubject({
      ...REPORT,
      spec: { ...REPORT.spec, owner: { apiVersion: 'apps/v1', kind: 'Deployment', name: 'web', uid: 'x' } },
    })
    expect(s?.kind).toBe('Deployment')
  })

  // The findings live in spec, not status, which is the reverse of every other
  // report-shaped object here.
  it('reads results and summary out of spec', () => {
    expect(getKyvernoReportResults(REPORT)).toHaveLength(1)
    expect(getKyvernoReportResults({ status: { results: [{}] } })).toEqual([])
    const s = getKyvernoReportSummary(REPORT)
    expect(s).toMatchObject({ fail: 1, pass: 2, total: 3 })
  })

  it('reports the worst outcome for the list badge', () => {
    expect(getKyvernoReportStatus(REPORT).level).toBe('unhealthy')
    expect(getKyvernoReportStatus({ spec: { summary: { pass: 4 } } }).text).toBe('4 pass')
    expect(getKyvernoReportStatus({ spec: {} }).text).toBe('No results')
  })

  // `new Date({seconds, nanos})` is Invalid Date, not an error.
  it('converts the seconds/nanos timestamp rather than passing it to Date', () => {
    expect(kyvernoReportTimestamp({ seconds: 1786622539, nanos: 0 })).toBe('2026-08-13T12:02:19.000Z')
    expect(kyvernoReportTimestamp({ seconds: 0 })).toBeUndefined()
    expect(kyvernoReportTimestamp(undefined)).toBeUndefined()
    expect(kyvernoReportTimestamp('2026-08-13T09:22:19Z')).toBe('2026-08-13T09:22:19Z')
  })
})
