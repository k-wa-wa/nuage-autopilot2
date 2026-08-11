import type { Meta, StoryObj } from '@storybook/react';
import { ActiveAgent } from './ActiveAgent';
import { mockStateActive, mockStateIdle } from '../../mocks/fixtures';

const meta: Meta<typeof ActiveAgent> = {
  title: 'Components/ActiveAgent',
  component: ActiveAgent,
  tags: ['autodocs'],
};

export default meta;
type Story = StoryObj<typeof ActiveAgent>;

export const Running: Story = {
  args: {
    active: mockStateActive.active,
    queueDepth: mockStateActive.queue_depth,
    activeHasLog: mockStateActive.active_has_log,
  },
};

export const Idle: Story = {
  args: {
    active: mockStateIdle.active,
    queueDepth: mockStateIdle.queue_depth,
    activeHasLog: mockStateIdle.active_has_log,
  },
};
