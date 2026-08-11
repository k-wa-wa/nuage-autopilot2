import type { Meta, StoryObj } from '@storybook/react';
import { LaneList } from './LaneList';
import { mockStateActive } from '../../mocks/fixtures';

const meta: Meta<typeof LaneList> = {
  title: 'Components/LaneList',
  component: LaneList,
  tags: ['autodocs'],
};

export default meta;
type Story = StoryObj<typeof LaneList>;

export const Default: Story = {
  args: {
    statuses: mockStateActive.meta.statuses,
    items: mockStateActive.items,
  },
};

export const Empty: Story = {
  args: {
    statuses: mockStateActive.meta.statuses,
    items: [],
  },
};
