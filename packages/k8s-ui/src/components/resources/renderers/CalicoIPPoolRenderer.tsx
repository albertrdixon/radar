import { Network } from 'lucide-react'
import { Property, PropertyList, Section } from '../../ui/drawer-components'
import {
  getCalicoIPPoolAllowedUses,
  getCalicoIPPoolBlockSize,
} from '../resource-utils-calico'

interface CalicoIPPoolRendererProps {
  data: any
}

export function CalicoIPPoolRenderer({ data }: CalicoIPPoolRendererProps) {
  const spec = data?.spec ?? {}

  return (
    <Section title="IP Pool" icon={Network}>
      <PropertyList>
        <Property label="Allowed Uses" value={getCalicoIPPoolAllowedUses(data)} />
        <Property label="Assignment Mode" value={spec.assignmentMode ?? 'Automatic'} />
        <Property label="Block Size" value={getCalicoIPPoolBlockSize(data)} />
        <Property label="CIDR" value={spec.cidr} />
        <Property label="IP-in-IP Mode" value={spec.ipipMode ?? 'Never'} />
        <Property label="VXLAN Mode" value={spec.vxlanMode ?? 'Never'} />
        <Property label="NAT Outgoing" value={spec.natOutgoing ? 'Yes' : 'No'} />
        <Property label="Disabled" value={spec.disabled ? 'Yes' : 'No'} />
        <Property label="BGP Export Disabled" value={spec.disableBGPExport ? 'Yes' : 'No'} />
        <Property label="Node Selector" value={spec.nodeSelector ?? 'all()'} />
        <Property label="Namespace Selector" value={spec.namespaceSelector} />
      </PropertyList>
    </Section>
  )
}
