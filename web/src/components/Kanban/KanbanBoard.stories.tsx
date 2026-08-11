import type { Meta, StoryObj } from '@storybook/react';
import { KanbanBoard } from './KanbanBoard';
import { mockStateActive } from '../../mocks/fixtures';

const meta: Meta<typeof KanbanBoard> = {
  title: 'Components/KanbanBoard',
  component: KanbanBoard,
  tags: ['autodocs'],
};

export default meta;
type Story = StoryObj<typeof KanbanBoard>;

export const Default: Story = {
  args: {
    statuses: mockStateActive.meta.statuses,
    items: mockStateActive.items,
  },
};
