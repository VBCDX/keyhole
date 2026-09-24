import type { ToolSnapshot } from './types'

/** Starter catalog — pre-built tools that install as editable drafts. */
export const CATALOG: { id: 'stripe' | 'github' | 'slack'; mark: string; blurb: string; tool: ToolSnapshot }[] = [
  {
    id: 'stripe',
    mark: 'S',
    blurb: 'List recent charges',
    tool: {
      internalName: 'stripe_charges',
      displayName: 'Stripe charges',
      description:
        'Lists recent charges from a Stripe account. Use when the user asks about recent payments, charge amounts, refunds, or payment status. Returns amounts in the account currency.',
      baseUrl: 'https://api.stripe.com',
      actions: [
        {
          id: 'a_charges',
          method: 'GET',
          path: '/v1/charges',
          summary: 'List recent charges',
          inputs: [
            { id: 'i_limit', name: 'limit', type: 'integer', required: false, description: 'How many charges to return (1–100). Defaults to 10.' },
          ],
        },
        {
          id: 'a_charge',
          method: 'GET',
          path: '/v1/charges/{id}',
          summary: 'Get one charge by id',
          inputs: [{ id: 'i_id', name: 'id', type: 'string', required: true, description: 'The charge id, starting with ch_.' }],
        },
      ],
      slots: [{ id: 's_stripe', name: 'stripe_secret', location: 'header', field: 'Authorization', scheme: 'Bearer' }],
      perMinute: 60,
      timeoutMs: 10000,
    },
  },
  {
    id: 'github',
    mark: 'G',
    blurb: 'List and create issues',
    tool: {
      internalName: 'github_issues',
      displayName: 'GitHub issues',
      description:
        "Lists and opens issues in the organization's GitHub repositories. Use when the user asks about open bugs, wants to file an issue, or needs the status of a ticket.",
      baseUrl: 'https://api.github.com',
      actions: [
        {
          id: 'a_list',
          method: 'GET',
          path: '/repos/{owner}/{repo}/issues',
          summary: 'List issues in a repository',
          inputs: [
            { id: 'i_owner', name: 'owner', type: 'string', required: true, description: 'Repository owner.' },
            { id: 'i_repo', name: 'repo', type: 'string', required: true, description: 'Repository name.' },
            { id: 'i_state', name: 'state', type: 'string', required: false, description: 'open, closed, or all.' },
          ],
        },
        {
          id: 'a_create',
          method: 'POST',
          path: '/repos/{owner}/{repo}/issues',
          summary: 'Open a new issue',
          inputs: [
            { id: 'i_owner2', name: 'owner', type: 'string', required: true, description: 'Repository owner.' },
            { id: 'i_repo2', name: 'repo', type: 'string', required: true, description: 'Repository name.' },
            { id: 'i_title', name: 'title', type: 'string', required: true, description: 'Issue title.' },
          ],
        },
      ],
      slots: [{ id: 's_gh', name: 'github_token', location: 'header', field: 'Authorization', scheme: 'Bearer' }],
      perMinute: 30,
      timeoutMs: 10000,
    },
  },
  {
    id: 'slack',
    mark: 'Sl',
    blurb: 'Post to channels',
    tool: {
      internalName: 'slack_messages',
      displayName: 'Slack messages',
      description:
        'Posts a message to a Slack channel the bot has joined. Use when the user asks to notify a team or share a short update. Never use for direct messages.',
      baseUrl: 'https://slack.com/api',
      actions: [
        {
          id: 'a_post',
          method: 'POST',
          path: '/chat.postMessage',
          summary: 'Post a message to a channel',
          inputs: [
            { id: 'i_channel', name: 'channel', type: 'string', required: true, description: 'Channel id or #name.' },
            { id: 'i_text', name: 'text', type: 'string', required: true, description: 'Message text.' },
          ],
        },
      ],
      slots: [{ id: 's_slack', name: 'SLACK_BOT_TOKEN', location: 'header', field: 'Authorization', scheme: 'Bearer' }],
      perMinute: 20,
      timeoutMs: 8000,
    },
  },
]

export const COMING_SOON_STORES = [
  'HashiCorp Vault',
  'AWS Secrets Manager',
  'Azure Key Vault',
  'Google Secret Manager',
  'CyberArk',
  '1Password',
]

/** Keyhole's fixed outgoing addresses to allow-list. */
export const EGRESS_IPS = ['52.34.18.9', '44.209.77.3']
