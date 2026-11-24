"""Run against standalone AST fixtures; no server compilation or runtime needed."""
import importlib.util
import pathlib
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).resolve().parents[2] / 'scripts' / 'audit-http-500-responses.py'
spec = importlib.util.spec_from_file_location('audit500', SCRIPT)
audit = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audit)

class AuditTests(unittest.TestCase):
    def run_fixture(self, adapter, factory='func NewGetThingErrorResponse(status int, body any) (any,error) { return GetThing500JSONResponse{},nil }', helpers=''):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            server = root/'internal/server'
            generated = root/'api/loomapi'
            server.mkdir(parents=True)
            generated.mkdir(parents=True)
            (server/'routes.go').write_text('package fixture\n'+adapter+'\n'+helpers)
            (generated/'api.gen.go').write_text('package loomapi\n'+factory)
            yaml = root/'openapi.yaml'
            yaml.write_text('paths:\n  /thing:\n    get:\n      operationId: getThing\n      responses:\n        "500": {}\n')
            return audit.audit_server(server, yaml)

    def test_direct_and_returned_helper_preserve_mapper_provenance(self):
        for adapter,helpers in [
            ('func GetThing() { status, failure := authoringErrorForOpenAPI(); return loomapi.NewGetThingErrorResponse(status, openAPIErrorResponseBody(status,failure)) }',''),
            ('func GetThing() { status,body:=mapServiceError(); return loomapi.NewGetThingErrorResponse(status,body) }',
             'type Service struct{}; func (*Service) GetThing() { return nil }'),
            ('func GetThing() { return dispatch() }','func dispatch() (loomapi.GetThingResponseObject,error) { status,body:=mapServiceError(); return loomapi.NewGetThingErrorResponse(status,body) }'),
            ('func GetThing() { fail:=func() (loomapi.GetThingResponseObject,error) {status,legacy:=explorerLegacyResponse(); return loomapi.NewGetThingErrorResponse(status,legacy)}; return fail() }',''),
        ]:
            with self.subTest(adapter=adapter):
                report=self.run_fixture(adapter,helpers=helpers)
                self.assertEqual(report['problems'],[])
                self.assertEqual(report['count'],1)

    def test_route_method_takes_precedence_over_same_named_helper(self):
        report = self.run_fixture(
            'type HTTPRoutes struct{}; func (*HTTPRoutes) GetThing() { return nil,nil }',
            helpers='func GetThing(){status,body:=mapServiceError();return loomapi.NewGetThingErrorResponse(status,body)}',
        )
        self.assertIn('getThing missing reachable mapped 500 response', report['problems'])

    def test_shadowed_binding_does_not_overwrite_outer_mapped_body(self):
        report = self.run_fixture('func GetThing(){status,body:=mapServiceError();{body:=nil;_=body};return loomapi.NewGetThingErrorResponse(status,body)}')
        self.assertEqual(report['problems'], [])
        unchanged=self.run_fixture('func GetThing(){status,body:=mapServiceError();{body:=body;_=body};return loomapi.NewGetThingErrorResponse(status,body)}')
        self.assertEqual(unchanged['problems'], [])
        terminal=self.run_fixture('func GetThing(){status,body:=mapServiceError();if failed {unknown(body);return nil,nil};return loomapi.NewGetThingErrorResponse(status,body)}')
        self.assertEqual(terminal['problems'], [])

    def test_factory_variant_must_be_returned_from_500_arm(self):
        adapter='func GetThing(){_,body:=mapServiceError();return loomapi.NewGetThingErrorResponse(500,body)}'
        for factory in [
            'func NewGetThingErrorResponse(status int, body any)(any,error){switch status{case 400:return GetThing500JSONResponse{},nil;case 500:return nil,nil};return nil,nil}',
            'func NewGetThingErrorResponse(status int, body any)(any,error){if status!=500{return GetThing500JSONResponse{},nil};return nil,nil}',
            'func NewGetThingErrorResponse(status int, body any)(any,error){if ok{return nil,nil}else{return nil,nil};return GetThing500JSONResponse{},nil}',
        ]:
            with self.subTest(factory=factory):
                self.assertTrue(self.run_fixture(adapter,factory=factory)['problems'])

    def test_report_count_and_mapper_counts_are_case_rows(self):
        report=self.run_fixture('func GetThing(){status,body:=mapServiceError();if failed{return loomapi.NewGetThingErrorResponse(status,body)};return loomapi.NewGetThingErrorResponse(status,body)}')
        self.assertEqual(report['problems'],[])
        self.assertEqual(report['count'],len(report['cases']))
        self.assertEqual(report['count'],2)
        self.assertEqual(sum(report['mappers'].values()),report['count'])
        self.assertEqual(report['covered500OperationCount'],1)

    def test_negative_controls(self):
        cases = [
            ('unused factory','func GetThing(){status,body:=mapServiceError(); _,_=loomapi.NewGetThingErrorResponse(status,body);return nil,nil}',''),
            ('discarded helper','func GetThing(){_,_=dispatch();return nil,nil}','func dispatch() (loomapi.GetThingResponseObject,error){status,body:=mapServiceError();return loomapi.NewGetThingErrorResponse(status,body)}'),
            ('unused closure','func GetThing(){fail:=func()(any,error){status,body:=mapServiceError();return loomapi.NewGetThingErrorResponse(status,body)};_=fail;return nil,nil}',''),
            ('wrong operation','func GetThing(){status,body:=mapServiceError();return loomapi.NewGetOtherErrorResponse(status,body)}',''),
            ('unmapped body','func GetThing(){status,body:=mapServiceError();_=body;return loomapi.NewGetThingErrorResponse(status,nil)}',''),
            ('overwritten body','func GetThing(){status,body:=mapServiceError();body=nil;return loomapi.NewGetThingErrorResponse(status,body)}',''),
            ('conditional overwrite','func GetThing(){status,body:=mapServiceError();if bad {body=nil};return loomapi.NewGetThingErrorResponse(status,body)}',''),
            ('constant non500','func GetThing(){_,body:=mapServiceError();return loomapi.NewGetThingErrorResponse(400,body)}',''),
            ('unreachable factory','func GetThing(){status,body:=mapServiceError();return nil,nil;return loomapi.NewGetThingErrorResponse(status,body)}',''),
            ('else overwrite','func GetThing(){status,body:=mapServiceError();if ok {} else {body=nil};return loomapi.NewGetThingErrorResponse(status,body)}',''),
            ('switch overwrite','func GetThing(){status,body:=mapServiceError();switch x {case 1:body=nil};return loomapi.NewGetThingErrorResponse(status,body)}',''),
            ('terminated if else','func GetThing(){status,body:=mapServiceError();if ok{return nil,nil}else{return nil,nil};return loomapi.NewGetThingErrorResponse(status,body)}',''),
            ('nested block overwrite','func GetThing(){status,body:=mapServiceError();{body=nil};return loomapi.NewGetThingErrorResponse(status,body)}',''),
            ('selector mutation','func GetThing(){status,body:=mapServiceError();body.Error.Message="PRIVATE-CANARY";return loomapi.NewGetThingErrorResponse(status,body)}',''),
            ('index mutation','func GetThing(){status,body:=mapServiceError();body["error"]="PRIVATE-CANARY";return loomapi.NewGetThingErrorResponse(status,body)}',''),
            ('aliased selector mutation','func GetThing(){status,body:=mapServiceError();alias:=body;alias.Error.Message="PRIVATE-CANARY";return loomapi.NewGetThingErrorResponse(status,body)}',''),
            ('write before shadow','func GetThing(){status,body:=mapServiceError();{body=nil;body,local:=other();_=body;_=local};return loomapi.NewGetThingErrorResponse(status,body)}',''),
            ('discarded mutating closure','func GetThing(){status,body:=mapServiceError();mutate:=func(){body.Error.Message="PRIVATE-CANARY"};mutate();return loomapi.NewGetThingErrorResponse(status,body)}',''),
            ('unknown mutating call','func GetThing(){status,body:=mapServiceError();err:=mutate(&body);_=err;return loomapi.NewGetThingErrorResponse(status,body)}',''),
            ('shallow shared shadow','func GetThing(){status,body:=mapServiceError();{body:=body;(*body.Error.Details)["reason"]="PRIVATE-CANARY"};return loomapi.NewGetThingErrorResponse(status,body)}',''),
            ('branch shallow shared shadow','func GetThing(){status,body:=mapServiceError();if bad{body:=body;(*body.Error.Details)["reason"]="PRIVATE-CANARY"};return loomapi.NewGetThingErrorResponse(status,body)}',''),
            ('switch shallow shared shadow','func GetThing(){status,body:=mapServiceError();switch x {case 1:body:=body;(*body.Error.Details)["reason"]="PRIVATE-CANARY"};return loomapi.NewGetThingErrorResponse(status,body)}',''),
            ('rebuilt mapped details','func GetThing(){status,body:=mapServiceError();body=loomapi.ServiceErrorResponse{Error:loomapi.ServiceErrorBody{Code:"INTERNAL_ERROR",Message:"PRIVATE-CANARY",Details:body.Error.Details}};return loomapi.NewGetThingErrorResponse(status,body)}',''),
            ('helper cycle','func GetThing(){return dispatch()}','func dispatch()(loomapi.GetThingResponseObject,error){return next()};func next()(loomapi.GetThingResponseObject,error){return dispatch()}'),
        ]
        for label,adapter,helpers in cases:
            with self.subTest(label=label):
                self.assertTrue(self.run_fixture(adapter,helpers=helpers)['problems'])

    def test_declared_factory_case_without_actual_return_is_rejected(self):
        report=self.run_fixture('func GetThing(){status,body:=mapServiceError();return loomapi.NewGetThingErrorResponse(status,body)}',factory='func NewGetThingErrorResponse(status int, body any)(any,error){_ = GetThing500JSONResponse{}; switch status{case 500:return nil,nil};return nil,nil}')
        self.assertTrue(report['problems'])

if __name__ == '__main__':
    unittest.main()
