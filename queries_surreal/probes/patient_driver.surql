RETURN (
  SELECT
    _key,
    id,
    project,
    auth_resource_path
  FROM Patient
  WHERE project = $project
    AND ($auth_resource_path = "" OR auth_resource_path = $auth_resource_path)
  ORDER BY _key ASC
  LIMIT IF $max_rows > 0 { $max_rows } ELSE { 1000 }
  TIMEOUT 10s
);
