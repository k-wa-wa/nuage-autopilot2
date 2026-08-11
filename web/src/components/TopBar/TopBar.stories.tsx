import type { Meta, StoryObj } from '@storybook/react';
import { TopBar } from './TopBar';
import { mockMeta } from '../../mocks/fixtures';

const meta: Meta<typeof TopBar> = {
  title: 'Components/TopBar',
  component: TopBar,
  tags: ['autodocs'],
};

export default meta;
type Story = StoryObj<typeof TopBar>;

export const Default: Story = {
  args: {
    meta: mockMeta,
    generatedAt: new Date().toISOString(),
    isRefreshing: false,
  },
};

export const Refreshing: Story = {
  args: {
    meta: mockMeta,
    generatedAt: new Date().toISOString(),
    isRefreshing: true,
  },
};
