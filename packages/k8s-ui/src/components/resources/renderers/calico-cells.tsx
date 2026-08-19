import { Badge } from '../../ui/Badge'
import { Tooltip } from '../../ui/Tooltip'
import {
  getCalicoPolicyNamespaceSelector,
  getCalicoPolicyRuleCount,
  getCalicoPolicySelector,
  getCalicoPolicyServiceAccountSelector,
  getCalicoPolicyTypes,
  getCalicoIPPoolAllowedUses,
  getCalicoIPPoolBlockSize,
  getCalicoIPPoolEncapsulation,
  isCalicoApiVersion,
  isCalicoPolicyResource,
} from '../resource-utils-calico'

export function CalicoPolicyCell({
  resource,
  column,
}: {
  resource: any
  column: string
}) {
  if (!isCalicoPolicyResource(resource))
    return <span className="text-sm text-theme-text-tertiary">-</span>

  switch (column) {
    case 'selector':
      return <SelectorCell value={getCalicoPolicySelector(resource)} />
    case 'namespaceSelector':
      return <SelectorCell value={getCalicoPolicyNamespaceSelector(resource)} />
    case 'serviceAccountSelector':
      return (
        <SelectorCell value={getCalicoPolicyServiceAccountSelector(resource)} />
      )
    case 'tier':
      return (
        <span className="text-sm text-theme-text-secondary">
          {resource.spec?.tier || 'default'}
        </span>
      )
    case 'order':
      return (
        <span className="text-sm text-theme-text-secondary">
          {resource.spec?.order ?? '-'}
        </span>
      )
    case 'types':
      return (
        <div className="flex flex-wrap gap-1">
          {getCalicoPolicyTypes(resource).map((type) => (
            <Badge key={type} tone="accent1" size="sm">
              {type}
            </Badge>
          ))}
        </div>
      )
    case 'stagedAction':
      return resource.spec?.stagedAction ? (
        <Badge tone="note" size="sm">
          {String(resource.spec.stagedAction)}
        </Badge>
      ) : (
        <span className="text-sm text-theme-text-tertiary">-</span>
      )
    case 'rules': {
      const { ingress, egress } = getCalicoPolicyRuleCount(resource)
      return (
        <span className="text-sm text-theme-text-secondary">
          {ingress}i / {egress}e
        </span>
      )
    }
    default:
      return <span className="text-sm text-theme-text-tertiary">-</span>
  }
}

function SelectorCell({ value }: { value: string }) {
  return (
    <Tooltip content={value}>
      <span className="text-sm text-theme-text-secondary truncate block font-mono">
        {value}
      </span>
    </Tooltip>
  )
}

/**
 * List cells for the Calico resources that describe the network itself rather
 * than policy: IPPool, HostEndpoint and Tier. The values that decide whether a
 * pool is in use — disabled, encapsulation, node selector — belong in the list,
 * not only behind a click on each row.
 */
export function CalicoInfraCell({
  resource,
  column,
}: {
  resource: any
  column: string
}) {
  if (!isCalicoApiVersion(resource?.apiVersion))
    return <span className="text-sm text-theme-text-tertiary">-</span>

  const spec = resource?.spec ?? {}
  switch (column) {
    case 'cidr':
      return <MonoCell value={spec.cidr} />
    case 'blockSize':
      return <TextCell value={getCalicoIPPoolBlockSize(resource)} />
    case 'encapsulation':
      return <TextCell value={getCalicoIPPoolEncapsulation(resource)} />
    case 'natOutgoing':
      return <YesNoCell value={spec.natOutgoing === true} />
    case 'disabled':
      return spec.disabled === true ? (
        <Badge severity="warning" size="sm">
          Disabled
        </Badge>
      ) : (
        <span className="text-sm text-theme-text-secondary">No</span>
      )
    case 'allowedUses':
      return <TextCell value={getCalicoIPPoolAllowedUses(resource)} />
    case 'nodeSelector':
      return <MonoCell value={spec.nodeSelector ?? 'all()'} />
    case 'node':
      return <TextCell value={spec.node} />
    case 'interfaceName':
      return <MonoCell value={spec.interfaceName} />
    case 'expectedIPs':
      return (
        <MonoCell
          value={
            Array.isArray(spec.expectedIPs)
              ? spec.expectedIPs.join(', ')
              : undefined
          }
        />
      )
    case 'profiles':
      return (
        <TextCell
          value={
            Array.isArray(spec.profiles) ? spec.profiles.join(', ') : undefined
          }
        />
      )
    case 'order':
      return <TextCell value={spec.order} />
    case 'defaultAction':
      return <TextCell value={spec.defaultAction ?? 'Deny'} />
    default:
      return <span className="text-sm text-theme-text-tertiary">-</span>
  }
}

function TextCell({ value }: { value: unknown }) {
  if (value === undefined || value === null || value === '')
    return <span className="text-sm text-theme-text-tertiary">-</span>
  return (
    <span className="text-sm text-theme-text-secondary">{String(value)}</span>
  )
}

function MonoCell({ value }: { value: unknown }) {
  if (value === undefined || value === null || value === '')
    return <span className="text-sm text-theme-text-tertiary">-</span>
  return <SelectorCell value={String(value)} />
}

function YesNoCell({ value }: { value: boolean }) {
  return (
    <span className="text-sm text-theme-text-secondary">
      {value ? 'Yes' : 'No'}
    </span>
  )
}
