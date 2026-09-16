import type { InteractionDecision, InteractionRequest } from '@/store/protocol'
import { PermissionCard } from './PermissionCard'
import { QuestionCard } from './QuestionCard'
import { PlanCard } from './PlanCard'

/**
 * The moments the agent is blocked on you. These are the only panels in the
 * interface that carry a signal colour of their own, because nothing else on
 * the page is a request.
 */
export function InteractionPanel({
  requests, onRespond,
}: {
  requests: InteractionRequest[]
  onRespond: (requestID: string, decision: InteractionDecision) => void
}) {
  if (!requests.length) return null
  return (
    <div className="my-5 space-y-3">
      {requests.map((request) => {
        const respond = (decision: InteractionDecision) => onRespond(request.request_id, decision)
        switch (request.payload.kind) {
          case 'permission':
            return <PermissionCard key={request.request_id} payload={request.payload} onRespond={respond} />
          case 'question':
            return <QuestionCard key={request.request_id} payload={request.payload} onRespond={respond} />
          case 'plan':
            return <PlanCard key={request.request_id} payload={request.payload} onRespond={respond} />
        }
      })}
    </div>
  )
}

export function Ask({ title, children, actions }: { title: string; children?: React.ReactNode; actions: React.ReactNode }) {
  return (
    <section className="overflow-hidden rounded-[6px] border border-waiting/40 bg-waiting/[0.07]">
      <header className="border-b border-waiting/25 px-3.5 py-2 text-[0.8125rem] font-medium text-ink">{title}</header>
      {children && <div className="px-3.5 py-2.5">{children}</div>}
      <footer className="flex flex-wrap gap-2 px-3.5 pb-3 pt-1">{actions}</footer>
    </section>
  )
}
