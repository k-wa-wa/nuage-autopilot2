import type { Meta, StoryObj } from '@storybook/react';
import { ItemDetailModal } from './ItemDetailModal';
import { mockItemDetail } from '../../mocks/fixtures';

const meta: Meta<typeof ItemDetailModal> = {
  title: 'Components/ItemDetailModal',
  component: ItemDetailModal,
  parameters: {
    layout: 'fullscreen',
  },
};

export default meta;
type Story = StoryObj<typeof ItemDetailModal>;

export const Default: Story = {
  args: {
    item: mockItemDetail.item,
    runs: mockItemDetail.runs,
    isLoadingRuns: false,
    onClose: () => {},
    onSelectRun: () => {},
  },
};
