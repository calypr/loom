package arango

func (c *Client) PreviewIndexes() PreviewIndexes {
	return PreviewIndexes{client: c}
}
