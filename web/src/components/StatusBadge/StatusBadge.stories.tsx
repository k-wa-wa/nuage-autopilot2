import type { Meta, StoryObj } from '@storybook/react';
import { StatusBadge, RunBadge } from './StatusBadge';

const meta: Meta<typeof StatusBadge> = {
  title: 'Components/StatusBadge',
  component: StatusBadge,
  tags: ['autodocs'],
};

export default meta;
type Story = StoryObj<typeof StatusBadge>;

export const AllStatuses: Story = {
  render: () => (
    <div className="flex flex-wrap gap-2 p-4 bg-slate-900 rounded-lg">
      <StatusBadge status="Inbox" />
      <StatusBadge status="Todo" />
      <StatusBadge status="In Progress" />
      <StatusBadge status="In Review" />
      <StatusBadge status="Verifying" />
      <StatusBadge status="Done" />
      <StatusBadge status="Blocked" />
    </div>
  ),
};

export const RunBadges: Story = {
  render: () => (
    <div className="flex flex-wrap gap-3 p-4 bg-slate-900 rounded-lg items-center">
      <RunBadge run={{ id: 1, repo: 'repo', issue: 1, phase: 'code', started_at: null, ended_at: null, result: '', has_log: true, running: true }} />
      <RunBadge run={{ id: 2, repo: 'repo', issue: 1, phase: 'code', started_at: '2026-08-11T00:00:00Z', ended_at: '2026-08-11T00:05:00Z', result: 'ok', has_log: true, running: false }} />
      <RunBadge run={{ id: 3, repo: 'repo', issue: 1, phase: 'code', started_at: '2026-08-11T00:00:00Z', ended_at: '2026-08-11T00:05:00Z', result: 'fail', has_log: true, running: false }} />
      <RunBadge run={{ id: 4, repo: 'repo', issue: 1, phase: 'code', started_at: '2026-08-11T00:00:00Z', ended_at: null, result: '', has_log: true, running: false }} />
      <RunBadge run={null} />
    </div>
  ),
};
