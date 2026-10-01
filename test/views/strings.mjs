// Expected strings, from the string catalog of docs/ui-contract.md (section
// 10, draft.8) with the owner decisions draft.9 writes in (D37 and the ones
// after it, marked "draft.9"). This is the one place to sync wording when the
// catalog changes: the checks never spell a catalog string themselves.

const plural = (n, one, other) => (n === 1 ? one : other);

export const S = {
  // 10.1
  title: 'Requests to approve',
  headerPersonal: (name) => `midPoint sees everything here as ${name}, this server's own account.`,
  headerPersonalHelp:
    'This server signs in to midPoint with its configured account, so midPoint sees that account on every request, whoever is using the assistant.',
  headerShared: (name) => `This server signs in to midPoint with a shared account, ${name}, so it can't show what's yours.`,

  // 10.2
  refresh: 'Refresh',
  refreshing: 'Refreshing…',
  asOf: /^As of \S/,
  askAssistantToRefresh: 'To update this view, ask the assistant to run it again.',
  readOnlyHost: 'This app can show this view but not act from it. To make changes, ask the assistant.',
  expand: 'Expand',
  collapse: 'Exit full screen',
  cancel: 'Cancel',
  showDetails: 'Show technical details',
  hideDetails: 'Hide technical details',
  showMore: 'Show more',
  showLess: 'Show less',
  whatsThis: "What's this?",
  you: 'you',
  personHidden: "a person you can't see in midPoint",
  personHiddenStart: "A person you can't see in midPoint",
  itemHidden: "an item you can't see in midPoint",
  request: 'the request',
  inherited: 'Comes with other access',
  inheritedVia: (source) => `Comes with ${source}`,
  detailsTool: (tool) => `Tool: ${tool}`,
  redacted: '[address removed]',
  openInMidpoint: 'Open in midPoint',
  openInMidpointLabel: (name) => `Open in midPoint: ${name}`,
  openInMidpointNote:
    '"Open in midPoint" opens midPoint\'s own pages. You may need to sign in there, and some pages need midPoint permissions you might not have.',

  // 10.3
  slow: 'Still waiting for midPoint…',
  cancelled: 'This request was cancelled.',
  cancelledReason: (reason) => `This request was cancelled: ${reason}`,
  versionMismatch: "This view doesn't match the server's version. The server's answer is shown as text below.",
  textOnly: "This answer can't be shown as a view. Here is the server's text.",

  // 10.4
  dryrun: {
    bannerTitle: 'Preview only',
    bannerBody: 'Changes are turned off on this server. Buttons show what would be sent to midPoint; nothing is changed.',
    submit: 'Show preview',
    resultTitle: 'Preview: nothing was changed',
    detailsRequest: 'Request that would be sent',
    detailsSummary: (summary) => `Server summary: ${summary}`,
  },

  // 10.5
  strip: {
    source: (source) => `Reported by ${source}`,
    sourceUnnamed: 'Reported by the connection between the assistant and midPoint',
    heldTitle: 'Waiting for approval',
    heldBody: (approver) => `This action is waiting for approval by ${approver}. It hasn't reached midPoint yet.`,
    heldBodyNoApprover: "This action is waiting for approval. It hasn't reached midPoint yet.",
    deniedTitle: 'Blocked',
    deniedBody: 'This action was blocked before it reached midPoint.',
    reason: (reason) => `Reason: ${reason}`,
    expires: /^Expires \S/,
    ref: (ref) => `Reference: ${ref}`,
  },

  // 10.6
  confirm: {
    commentOptional: 'Comment (optional)',
    commentRequired: 'Reason (required)',
    requiredError: 'Enter a reason to continue.',
    working: 'Working…',
    approveTitle: (change, role, requestee) =>
      change === 'add'
        ? `Approve ${role} for ${requestee}?`
        : change === 'delete'
          ? `Approve removing ${role} from ${requestee}?`
          : `Approve this request about ${role}?`,
    // draft.9 (owner): replaces draft.8's "This is the last approval needed:
    // midPoint makes the change when you approve."
    approveBodyFinal: "This is the last approval step. Once it's approved, midPoint makes the change.",
    approveBodyMore: 'midPoint makes the change only after the other approvals are in.',
    approveBodyUnknown: 'midPoint records your approval and the request moves on.',
    approveSubmit: 'Approve',
    rejectTitle: (change, role, requestee) =>
      change === 'add'
        ? `Reject ${role} for ${requestee}?`
        : change === 'delete'
          ? `Reject removing ${role} from ${requestee}?`
          : `Reject this request about ${role}?`,
    rejectBody: (requester) => `${requester} can see your reason.`,
    rejectSubmit: 'Reject',
    // Draft.8 dialog rows, which approve and reject must not show (D21).
    rows: ['Role', 'For', 'From', 'Request', 'How long'],
  },

  // 10.7, keyed by the stable codes of 6.8
  errorByCode: {
    'shared-credential':
      "This server signs in to midPoint with a shared account, so it can't answer for you personally. Ask your midPoint administrator to set up per-person sign-in.",
    'not-requestable': "This role can't be requested: midPoint's catalog doesn't offer it for request.",
    'invalid-field': 'Some request details are missing or not valid. Check the marked field.',
    'invalid-validity': "The chosen dates aren't valid. Check how long the role should be valid and try again.",
    'not-in-inbox': 'This request is no longer waiting for your decision.',
    'already-decided': 'This step of the request has already been decided.',
    'request-closed': 'This request is already closed: it was decided or withdrawn.',
    'not-your-request': 'Only the person who made a request can withdraw it.',
    'not-assigned': 'This role is no longer directly assigned to this person.',
    'invalid-input': 'The server rejected the request as incomplete or invalid.',
    'not-authorized': "midPoint says you aren't allowed to do this.",
    'not-found': "midPoint couldn't find this item. It may have been deleted.",
    'midpoint-unavailable': "midPoint didn't answer. Try again in a moment.",
    internal: "midPoint couldn't complete this.",
  },
  hostRefused: "The assistant app didn't allow this action.",

  // 10.8
  status: { waiting: 'Waiting', approved: 'Approved', rejected: 'Rejected', closed: 'Closed', personDisabled: 'Account turned off' },

  // 10.9 (the forms the inbox needs)
  time: { todayAt: (t) => `today, ${t}`, tomorrowAt: (t) => `tomorrow, ${t}`, today: 'today', tomorrow: 'tomorrow' },

  // 10.10, as draft.9 (owner) has it for the inbox: one line per step with
  // no people, and the step's state; draft.8's "{who}", "Decided by",
  // "Closed …" and "Comment from" are gone from the inbox's Details.
  timeline: {
    stage: (n) => `Step ${n}`,
    stageNamed: (n, name) => `Step ${n}, ${name}`,
    // What a step line may say about the step, after its name.
    states: ['Waiting', 'Approved', 'Rejected', 'Closed'],
    // draft.8 timeline texts the inbox no longer shows.
    forbidden: [/, (both|all) needed\b/, /the first decision counts/, /^Decided by /m, /^Closed /m, /^Comment from /m],
  },

  // 10.11
  inbox: {
    loading: 'Loading your approval inbox…',
    summary: (n) => `${n} ${plural(n, 'request', 'requests')} waiting for your decision`,
    emptyTitle: 'Nothing is waiting for your decision.',
    emptyPersonal: (name) => `This inbox belongs to ${name}, the account this server signs in with.`,
    change: {
      add: (requestee, target) => `${requestee} → ${target}`,
      delete: (requestee, target) => `${requestee}: remove ${target}`,
      modify: (requestee, target) => `${requestee}: change ${target}`,
      unknown: (requestee, target) => `${requestee}: request about ${target}`,
    },
    changeAddLabel: (requestee, target) => `${requestee}, access to ${target}`,
    requestedBy: (requester) => `Requested by ${requester}`,
    selfRequested: (requestee) => `Requested by ${requestee} for themselves`,
    requestedAt: /^Requested .+\(.+\)$/,
    deadline: (time) => `Decide by ${time}`,
    overduePrefix: 'Decision overdue since ',
    justification: (requester) => `Reason given by ${requester}`,
    noReason: 'No reason given',
    cantApprove:
      "You can't see who this is for, so you can't approve it here. You can still reject it, or ask your midPoint administrator why this person is hidden from you.",
    risk: (level) => `Risk: ${level}`,
    why: {
      manager: (requestee) => `You're asked because you manage ${requestee}.`,
      roleApprover: (target) => `You're asked because you approve requests for ${target}.`,
      roleOwner: (target) => `You're asked because you own ${target}.`,
      step: (name) => `You're asked for the "${name}" step.`,
      assigned: 'midPoint sent this request to you.',
    },
    currentRoles: (requestee, n) =>
      `${requestee} has ${n} ${plural(n, 'role', 'roles')} in effect now, including ones that come with other roles`,
    currentRolesHidden: (requestee) => `You can't see ${requestee}'s current roles.`,
    currentRolesNone: (requestee) => `${requestee} has no roles now.`,
    approve: 'Approve',
    reject: 'Reject',
    approveLabel: (target, requestee) => `Approve ${target} for ${requestee}`,
    rejectLabel: (target, requestee) => `Reject ${target} for ${requestee}`,
    details: 'Details',
    hideDetails: 'Hide details',
    history: 'Approval steps',
    previewApprove: 'Preview approval',
    previewReject: 'Preview rejection',
    previewApproveLabel: (target, requestee) => `Preview approval of ${target} for ${requestee}`,
    previewRejectLabel: (target, requestee) => `Preview rejection of ${target} for ${requestee}`,
    outcome: {
      approvedClosed: 'Approved. The request is complete.',
      rejectedClosed: 'Rejected. The request is closed.',
      // draft.9 (owner): no "Approved. The request now waits for {names}.";
      // an approval with the case still open reads approvedOpen, whoever is
      // next.
      approvedOpen: 'Approved. The request continues in midPoint.',
      rejectedOpen: 'Rejected. midPoint is still processing the request.',
      decidedByOther: (outcome) =>
        `midPoint already shows this step as ${outcome === 'approve' ? 'approved' : outcome === 'reject' ? 'rejected' : 'decided'}. Someone else probably decided first.`,
      unconfirmed: "Sent to midPoint, but it hasn't recorded a decision yet. Refresh to check.",
    },
  },

  // 10.18
  validity: {
    permanent: 'No end date',
    // Any requested-access phrase of 4.5 (the clock-dependent ones by shape).
    anyRequest: /^(No end date|Access for \d+ days? \(ends .+\)|Access from .+ for \d+ days? \(ends .+\)|Access from .+, no end date|The requested end date has passed \(.+\))$/,
    days: (n) => new RegExp(`^Access for ${n} ${n === 1 ? 'day' : 'days'} \\(ends .+\\)$`),
  },

  // Owner decision D37 ("you approve your thing only"), overriding draft.8:
  // a step line on each card ("Step 1 of 1" included; none only when the
  // count is unknown), no "who else decides" line, and the approve sentence
  // chosen by steps only.
  d37: {
    step: (number, count) => `Step ${number} of ${count}`,
    // draft.8 inbox.approvers.* and inbox.steps.more, which must not appear.
    forbidden: [
      /can also decide this\. The first decision counts\./,
      /^Needs approval from (both|each of) /m,
      /Also asked to decide: /,
      /You're the only one asked at this step\./,
      /more approval steps? follows?\./,
    ],
    // draft.9 (owner): no approver names in an outcome.
    forbiddenOutcome: [/now waits for/],
  },
};

// isBodyFinal reports whether a dialog line is the last-step sentence.
export function isBodyFinal(text) {
  return String(text ?? '').trim() === S.confirm.approveBodyFinal;
}
