import type { Meta, StoryObj } from '@storybook/react';
import { LogViewer } from './LogViewer';
import { mockRunDetail } from '../../mocks/fixtures';

const meta: Meta<typeof LogViewer> = {
  title: 'Components/LogViewer',
  component: LogViewer,
  tags: ['autodocs'],
};

export default meta;
type Story = StoryObj<typeof LogViewer>;

export const Completed: Story = {
  args: {
    run: mockRunDetail.run,
    log: mockRunDetail.log,
    isStreaming: false,
    onBack: () => {},
  },
};

export const Streaming: Story = {
  args: {
    run: { ...mockRunDetail.run, running: true, ended_at: null },
    log: mockRunDetail.log,
    isStreaming: true,
    onBack: () => {},
  },
};
