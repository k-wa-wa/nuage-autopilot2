import type { Meta, StoryObj } from '@storybook/react';
import { TopBar } from './TopBar';

const meta: Meta<typeof TopBar> = {
  title: 'Components/TopBar',
  component: TopBar,
  tags: ['autodocs'],
};

export default meta;
type Story = StoryObj<typeof TopBar>;

export const Default: Story = {
  args: {
    generatedAt: new Date().toISOString(),
    isRefreshing: false,
  },
};

export const Refreshing: Story = {
  args: {
    generatedAt: new Date().toISOString(),
    isRefreshing: true,
  },
};
